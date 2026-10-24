// Package anilist implements the official AniList GraphQL API.
package anilist

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const Endpoint = "https://graphql.anilist.co"
const maxResponse = 4 << 20

var ErrAuth = errors.New("AniList authentication expired or denied; run ani-sync login")

type Status string

const (
	Current   Status = "CURRENT"
	Planning  Status = "PLANNING"
	Completed Status = "COMPLETED"
	Paused    Status = "PAUSED"
	Dropped   Status = "DROPPED"
	Repeating Status = "REPEATING"
)

func (s Status) Valid() bool {
	switch s {
	case Current, Planning, Completed, Paused, Dropped, Repeating:
		return true
	}
	return false
}

type Title struct {
	English string `json:"english"`
	Romaji  string `json:"romaji"`
	Native  string `json:"native"`
}

func (t Title) Preferred(language string) string {
	first := t.English
	if language == "romaji" {
		first = t.Romaji
	}
	if language == "native" {
		first = t.Native
	}
	for _, s := range []string{first, t.Romaji, t.English, t.Native} {
		if s != "" {
			return s
		}
	}
	return "Untitled"
}

type Media struct {
	ID         int        `json:"id"`
	Title      Title      `json:"title"`
	Episodes   int        `json:"episodes"`
	Synonyms   []string   `json:"synonyms"`
	CoverImage CoverImage `json:"coverImage"`
}
type CoverImage struct {
	Large string `json:"large"`
}
type Entry struct {
	ID       int    `json:"id"`
	Progress int    `json:"progress"`
	Status   Status `json:"status"`
	Media    Media  `json:"media"`
}
type User struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}
type request[V any] struct {
	Query     string `json:"query"`
	Variables V      `json:"variables"`
}
type gqlError struct {
	Status int `json:"status"`
}
type response[D any] struct {
	Data   *D         `json:"data"`
	Errors []gqlError `json:"errors"`
}
type pageInfo struct {
	HasNextPage bool `json:"hasNextPage"`
}

// Client is intended for sequential use. It honors rate headers across requests.
type Client struct {
	http            *http.Client
	endpoint, token string
	next            time.Time
}

func New(token string) *Client {
	return &Client{http: &http.Client{Timeout: 25 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, endpoint: Endpoint, token: token}
}

// WithTransport supplies an HTTP transport for dependency injection. Production
// callers use New, which retains Go's verified TLS and standard transport.
func WithTransport(token string, transport http.RoundTripper) *Client {
	c := New(token)
	c.http.Transport = transport
	return c
}

func wait(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
func retryDelay(h http.Header) time.Duration {
	delay := time.Minute
	if v, err := strconv.Atoi(h.Get("Retry-After")); err == nil && v >= 0 {
		if v > 120 {
			return 3 * time.Minute
		}
		delay = time.Duration(v) * time.Second
	} else if t, err := http.ParseTime(h.Get("Retry-After")); err == nil {
		delay = time.Until(t)
	}
	if v, err := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64); err == nil {
		if d := time.Until(time.Unix(v, 0)); d > delay {
			delay = d
		}
	}
	if delay < 0 {
		delay = 0
	}
	return delay
}

func do[V, D any](ctx context.Context, c *Client, q string, v V, out *D) error {
	body, err := json.Marshal(request[V]{q, v})
	if err != nil {
		return errors.New("encode GraphQL request")
	}
	for attempt := 0; attempt < 3; attempt++ {
		if d := time.Until(c.next); d > 2*time.Minute {
			return errors.New("AniList rate limit; retry later")
		} else if err := wait(ctx, d); err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
		if err != nil {
			return errors.New("invalid API endpoint")
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "ani-sync/0.1")
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}
		res, err := c.http.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errors.New("AniList network request failed (check connection and TLS)")
		}
		b, readErr := io.ReadAll(io.LimitReader(res.Body, maxResponse+1))
		res.Body.Close()
		if res.StatusCode == 401 || res.StatusCode == 403 {
			return ErrAuth
		}
		if res.StatusCode == 429 {
			c.next = time.Now().Add(retryDelay(res.Header))
			continue
		}
		if res.Header.Get("X-RateLimit-Remaining") == "0" {
			c.next = time.Now().Add(retryDelay(res.Header))
		}
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			return fmt.Errorf("AniList HTTP error %d", res.StatusCode)
		}
		if readErr != nil {
			return errors.New("read AniList response failed")
		}
		if len(b) > maxResponse {
			return errors.New("AniList response exceeds size limit")
		}
		var envelope response[D]
		if json.Unmarshal(b, &envelope) != nil {
			return errors.New("invalid AniList JSON response")
		}
		if len(envelope.Errors) > 0 {
			limited := false
			for _, e := range envelope.Errors {
				if e.Status == 401 || e.Status == 403 {
					return ErrAuth
				}
				if e.Status == 429 {
					limited = true
				}
			}
			if limited {
				c.next = time.Now().Add(retryDelay(res.Header))
				continue
			}
			// Do not include remote error messages: these can reflect credentials or input.
			return errors.New("AniList GraphQL request failed")
		}
		if envelope.Data == nil {
			return errors.New("AniList returned no data")
		}
		*out = *envelope.Data
		return nil
	}
	return errors.New("AniList rate limit persisted; retry later")
}

const mediaFields = `id title { english romaji native } episodes synonyms coverImage { large }`
const entryFields = `id progress status media { ` + mediaFields + ` }`

func (c *Client) Viewer(ctx context.Context) (User, error) {
	var d struct{ Viewer *User }
	err := do(ctx, c, `query { Viewer { id name } }`, struct{}{}, &d)
	if err != nil {
		return User{}, err
	}
	if d.Viewer == nil || d.Viewer.ID <= 0 {
		return User{}, errors.New("AniList returned no viewer")
	}
	return *d.Viewer, nil
}

func (c *Client) List(ctx context.Context, user int, status Status) ([]Entry, error) {
	if user <= 0 || status != "" && !status.Valid() {
		return nil, errors.New("invalid watchlist filter")
	}
	type vars struct {
		User   int     `json:"user"`
		Page   int     `json:"page"`
		Status *Status `json:"status,omitempty"`
	}
	var filter *Status
	var statusVariable, statusArgument string
	if status != "" {
		filter = &status
		statusVariable = ",$status:MediaListStatus"
		statusArgument = ",status:$status"
	}
	// An absent filter must not become an explicit null status in GraphQL.
	query := `query($user:Int!,$page:Int!` + statusVariable + `){Page(page:$page,perPage:50){pageInfo{hasNextPage} mediaList(userId:$user,type:ANIME` + statusArgument + `,sort:MEDIA_ID){` + entryFields + `}}}`
	entries := []Entry{}
	seen := map[int]bool{}
	for page := 1; page <= 100; page++ {
		var d struct {
			Page *struct {
				PageInfo  *pageInfo `json:"pageInfo"`
				MediaList []Entry   `json:"mediaList"`
			}
		}
		err := do(ctx, c, query, vars{user, page, filter}, &d)
		if err != nil {
			return nil, err
		}
		if d.Page == nil || d.Page.PageInfo == nil {
			return nil, errors.New("missing watchlist page")
		}
		for _, entry := range d.Page.MediaList {
			if entry.ID <= 0 || entry.Media.ID <= 0 || entry.Progress < 0 || !entry.Status.Valid() {
				return nil, errors.New("invalid watchlist entry")
			}
			if !seen[entry.ID] {
				entries = append(entries, entry)
				seen[entry.ID] = true
			}
		}
		if !d.Page.PageInfo.HasNextPage {
			return entries, nil
		}
		if len(d.Page.MediaList) == 0 {
			return nil, errors.New("empty non-final watchlist page")
		}
	}
	return nil, errors.New("AniList pagination depth limit reached (5000 entries); use a status filter")
}

func (c *Client) Search(ctx context.Context, query string, page int) ([]Media, bool, error) {
	if strings.TrimSpace(query) == "" || len(query) > 300 || page < 1 || page > 100 {
		return nil, false, errors.New("search needs a title (1–300 bytes) and page 1–100")
	}
	var d struct {
		Page *struct {
			PageInfo *pageInfo `json:"pageInfo"`
			Media    []Media
		}
	}
	v := struct {
		Search string `json:"search"`
		Page   int    `json:"page"`
	}{query, page}
	err := do(ctx, c, `query($search:String!,$page:Int!){Page(page:$page,perPage:50){pageInfo{hasNextPage} media(search:$search,type:ANIME){`+mediaFields+`}}}`, v, &d)
	if err != nil {
		return nil, false, err
	}
	if d.Page == nil || d.Page.PageInfo == nil {
		return nil, false, errors.New("missing search page")
	}
	return d.Page.Media, d.Page.PageInfo.HasNextPage, nil
}

func (c *Client) Entry(ctx context.Context, user, media int) (Entry, error) {
	if user <= 0 || media <= 0 {
		return Entry{}, errors.New("invalid user or media ID")
	}
	var d struct{ MediaList *Entry }
	v := struct {
		User  int `json:"user"`
		Media int `json:"media"`
	}{user, media}
	err := do(ctx, c, `query($user:Int!,$media:Int!){MediaList(userId:$user,mediaId:$media,type:ANIME){`+entryFields+`}}`, v, &d)
	if err != nil {
		return Entry{}, err
	}
	if d.MediaList == nil || d.MediaList.ID <= 0 || d.MediaList.Media.ID != media || d.MediaList.Progress < 0 || !d.MediaList.Status.Valid() {
		return Entry{}, errors.New("anime is missing from your AniList list or returned invalid data")
	}
	return *d.MediaList, nil
}

func (c *Client) SetProgress(ctx context.Context, id, value int) error {
	if id <= 0 || value < 0 || value > 2147483647 {
		return errors.New("invalid progress mutation")
	}
	var d struct {
		SaveMediaListEntry *struct {
			ID       int
			Progress int
		}
	}
	v := struct {
		ID       int `json:"id"`
		Progress int `json:"progress"`
	}{id, value}
	err := do(ctx, c, `mutation($id:Int!,$progress:Int!){SaveMediaListEntry(id:$id,progress:$progress){id progress}}`, v, &d)
	if err != nil {
		return err
	}
	if d.SaveMediaListEntry == nil || d.SaveMediaListEntry.ID != id || d.SaveMediaListEntry.Progress != value {
		return errors.New("AniList did not confirm the requested progress")
	}
	return nil
}
