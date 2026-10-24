package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lfreixial/ani-sync/internal/anilist"
	"github.com/lfreixial/ani-sync/internal/auth"
	"github.com/lfreixial/ani-sync/internal/config"
	"github.com/lfreixial/ani-sync/internal/matching"
	"github.com/lfreixial/ani-sync/internal/playback"
	"github.com/lfreixial/ani-sync/internal/progress"
	"github.com/lfreixial/ani-sync/internal/testutil"
	"github.com/lfreixial/ani-sync/internal/tui"
)

type rewriteTransport struct{ base http.RoundTripper }

func (r rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = "http"
	return r.base.RoundTrip(clone)
}

type harness struct {
	app                       *App
	out                       *bytes.Buffer
	watched, mutations, plays int
	playErr                   error
	searches                  []string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	s, err := config.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := auth.Save(s, auth.Token{AccessToken: "test-secret", ExpiresAt: time.Now().Add(time.Hour), UserID: 42}); err != nil {
		t.Fatal(err)
	}
	h := &harness{out: new(bytes.Buffer), watched: 2}
	client := testutil.HTTPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query     string
			Variables struct {
				Progress int
				Status   json.RawMessage
				Search   string
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		switch {
		case strings.Contains(req.Query, "Viewer"):
			fmt.Fprint(w, `{"data":{"Viewer":{"id":42,"name":"Tester"}}}`)
		case strings.HasPrefix(req.Query, "mutation"):
			h.mutations++
			h.watched = req.Variables.Progress
			fmt.Fprintf(w, `{"data":{"SaveMediaListEntry":{"id":7,"progress":%d}}}`, h.watched)
		case strings.Contains(req.Query, "Page"):
			if strings.Contains(req.Query, "mediaList") {
				// Explicit null is a filter value, not an unfiltered list.
				if string(req.Variables.Status) == "null" {
					fmt.Fprint(w, `{"data":{"Page":{"pageInfo":{"hasNextPage":false},"mediaList":[]}}}`)
					return
				}
				fmt.Fprintf(w, `{"data":{"Page":{"pageInfo":{"hasNextPage":false},"mediaList":[{"id":7,"progress":%d,"status":"CURRENT","media":{"id":9,"title":{"english":"Frieren","romaji":"Sousou"},"episodes":12}}]}}}`, h.watched)
			} else {
				h.searches = append(h.searches, req.Variables.Search)
				if r.Header.Get("Authorization") != "" {
					t.Error("public search must not send credentials")
				}
				fmt.Fprint(w, `{"data":{"Page":{"pageInfo":{"hasNextPage":false},"media":[{"id":9,"title":{"english":"Frieren"},"episodes":12}]}}}`)
			}
		case strings.Contains(req.Query, "MediaList(userId"):
			fmt.Fprintf(w, `{"data":{"MediaList":{"id":7,"progress":%d,"status":"CURRENT","media":{"id":9,"title":{"english":"Frieren","romaji":"Sousou"},"episodes":12}}}}`, h.watched)
		default:
			t.Error("unexpected query", req.Query)
			w.WriteHeader(500)
		}
	}))
	h.app = &App{Store: s, Config: config.Config{TitleLanguage: "english"}, UI: &tui.UI{Out: h.out, Err: h.out, In: strings.NewReader("")}, NewClient: func(token string) *anilist.Client {
		return anilist.WithTransport(token, rewriteTransport{client.Transport})
	}, Play: func(_ context.Context, q string, episode int, _ playback.Streams) error {
		h.plays++
		if q != "Sousou" || episode != 3 {
			t.Errorf("play %q %d", q, episode)
		}
		return h.playErr
	}}
	return h
}

func TestWatchManualConfirmation(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		playErr     error
		want        int
	}{
		{"complete", "y\ny\n", nil, 1}, {"decline", "n\n", nil, 0}, {"interrupted", "", context.Canceled, 0}, {"failed", "", errors.New("player failed"), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.playErr = tc.playErr
			h.app.UI.In = strings.NewReader(tc.input)
			h.app.UI.Interactive = true
			err := h.app.Execute(context.Background(), []string{"watch", "--id", "9", "--episode", "3", "--alias", "Sousou"})
			if (err != nil) != (tc.playErr != nil) {
				t.Fatal(err)
			}
			if h.mutations != tc.want {
				t.Fatal(h.mutations)
			}
			pending, err := (&progress.Service{Store: h.app.Store}).Pending()
			if err != nil || len(pending) != 0 {
				t.Fatal(pending, err)
			}
			if tc.want == 1 {
				aliases, err := matching.Load(h.app.Store)
				if err != nil || aliases.Get(9) != "Sousou" {
					t.Fatal(aliases, err)
				}
			}
		})
	}
}

func TestWatchWithoutArguments(t *testing.T) {
	h := newHarness(t)
	h.app.UI.Interactive = true
	// Accept the next episode and source title, then decline completion.
	h.app.UI.In = strings.NewReader("\n\nn\n")
	if err := h.app.Execute(context.Background(), []string{"watch"}); err != nil {
		t.Fatal(err)
	}
	if h.plays != 1 || h.mutations != 0 {
		t.Fatalf("plays=%d mutations=%d", h.plays, h.mutations)
	}
}
func TestAutoCannotMarkOnExit(t *testing.T) {
	h := newHarness(t)
	err := h.app.Execute(context.Background(), []string{"watch", "--id", "9", "--episode", "3", "--alias", "Sousou", "--auto", "--non-interactive"})
	if err != nil || h.mutations != 0 {
		t.Fatal(err, h.mutations)
	}
	if !strings.Contains(h.out.String(), "Manual completion") {
		t.Fatal(h.out.String())
	}
}
func TestExplicitScriptProgress(t *testing.T) {
	h := newHarness(t)
	if err := h.app.Execute(context.Background(), []string{"progress", "set", "--id", "9", "--episodes", "3", "--yes", "--non-interactive"}); err != nil || h.mutations != 1 {
		t.Fatal(err, h.mutations)
	}
	if err := h.app.Execute(context.Background(), []string{"progress", "set", "--id", "9", "--episodes", "1", "--yes", "--non-interactive"}); err == nil {
		t.Fatal("implicit decrease")
	}
	if err := h.app.Execute(context.Background(), []string{"progress", "set", "--id", "9", "--episodes", "1", "--yes", "--allow-decrease", "--non-interactive"}); err != nil || h.watched != 1 {
		t.Fatal(err, h.watched)
	}
}
func TestParsingRejectsIgnoredFlags(t *testing.T) {
	for _, args := range [][]string{{"watch", "--yes"}, {"list", "--confirm-completed"}, {"status", "extra"}, {"unknown"}, {"watch", "--id", "-1"}} {
		if _, _, _, err := parse(args); err == nil {
			t.Fatal(args)
		}
	}
}
func TestLogout(t *testing.T) {
	h := newHarness(t)
	if err := h.app.Execute(context.Background(), []string{"logout"}); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Load(h.app.Store); err == nil {
		t.Fatal("credentials retained")
	}
}

func TestReadCommands(t *testing.T) {
	for _, args := range [][]string{{"status", "--json"}, {"list", "--json"}, {"watching"}, {"completed"}, {"planning"}, {"progress"}, {"search", "--json", "Frieren"}, {"config"}, {"config", "set", "client-id", "123"}, {"config", "set", "title-language", "romaji"}, {"sync"}, {"sync", "--discard", "--yes"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			h := newHarness(t)
			if err := h.app.Execute(context.Background(), args); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(h.out.String(), "test-secret") {
				t.Fatal("token leaked")
			}
		})
	}
	if command, _, _, err := parse([]string{"doctor"}); err != nil || command != "doctor" {
		t.Fatal(command, err)
	}
}

func TestSearchTitleInput(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		args        []string
		interactive bool
		prompt      bool
		wantError   bool
	}{
		{"prompt", "Frieren\n", []string{"search"}, true, true, false},
		{"trim prompt", "  Frieren  \n", []string{"search"}, true, true, false},
		{"explicit title", "", []string{"search", "Frieren"}, true, false, false},
		{"script title", "", []string{"search", "--non-interactive", "Frieren"}, false, false, false},
		{"empty prompt", "\n", []string{"search"}, true, true, true},
		{"missing script title", "", []string{"search"}, false, false, true},
		{"non-interactive flag", "", []string{"search", "--non-interactive"}, true, false, true},
		{"json missing title", "", []string{"search", "--json"}, true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.app.UI.Interactive = tc.interactive
			h.app.UI.In = strings.NewReader(tc.input)
			err := h.app.Execute(context.Background(), tc.args)
			if tc.wantError {
				if err == nil || !strings.Contains(err.Error(), `ani-sync search "Frieren"`) || len(h.searches) != 0 {
					t.Fatalf("error=%v searches=%v", err, h.searches)
				}
			} else if err != nil || len(h.searches) != 1 || h.searches[0] != "Frieren" || !strings.Contains(h.out.String(), "Frieren") {
				t.Fatalf("error=%v searches=%v output=%q", err, h.searches, h.out.String())
			}
			if strings.Contains(h.out.String(), "Anime title to search") != tc.prompt {
				t.Fatalf("unexpected prompt output: %q", h.out.String())
			}
		})
	}
}

func TestContinueAndTitleSelection(t *testing.T) {
	h := newHarness(t)
	h.app.UI.Interactive = true
	h.app.UI.In = strings.NewReader("n\n")
	if err := h.app.Execute(context.Background(), []string{"watch", "--episode", "3", "--alias", "Sousou", "Frieren"}); err != nil {
		t.Fatal(err)
	}
	aliases := matching.Aliases{}
	if err := aliases.Save(h.app.Store, 9, "Sousou"); err != nil {
		t.Fatal(err)
	}
	h.app.UI.Interactive = false
	if err := h.app.Execute(context.Background(), []string{"continue", "--non-interactive"}); err != nil || h.plays != 2 {
		t.Fatal(err, h.plays)
	}
	if err := h.app.Store.Write("last.json", lastPlayed{99, 9}); err != nil {
		t.Fatal(err)
	}
	if err := h.app.Execute(context.Background(), []string{"continue"}); err == nil {
		t.Fatal("cross-account continue")
	}
}
