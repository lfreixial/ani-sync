package anilist

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/lfreixial/ani-sync/internal/testutil"
	"net/http"
	"strings"
	"testing"
	"time"
)

func testClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	c := New("secret")
	c.endpoint = "http://test.local"
	c.http = testutil.HTTPClient(t, h)
	return c
}

func TestPagination(t *testing.T) {
	calls := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing auth")
		}
		var body request[struct {
			Page int
			User int
		}]
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Variables.User != 42 || body.Variables.Page != calls {
			t.Error(body)
		}
		fmt.Fprintf(w, `{"data":{"Page":{"pageInfo":{"hasNextPage":%t},"mediaList":[{"id":%d,"progress":2,"status":"CURRENT","media":{"id":%d,"episodes":12,"title":{"romaji":"Sousou","english":null,"native":"葬送"}}}]}}}`, calls == 1, calls, calls)
	})
	entries, err := c.List(context.Background(), 42, Current)
	if err != nil || len(entries) != 2 || calls != 2 {
		t.Fatalf("%+v %v %d", entries, err, calls)
	}
	if entries[0].Media.Title.Preferred("english") != "Sousou" {
		t.Fatal("title fallback")
	}
}

func TestListStatusFilters(t *testing.T) {
	statuses := []Status{Current, Planning, Completed, Paused, Dropped, Repeating}
	for _, filter := range append([]Status{""}, statuses...) {
		name := string(filter)
		if name == "" {
			name = "all statuses"
		}
		t.Run(name, func(t *testing.T) {
			want := statuses
			if filter != "" {
				want = []Status{filter}
			}
			calls := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				var body request[struct {
					Page   int
					Status json.RawMessage
				}]
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				if filter == "" {
					if len(body.Variables.Status) != 0 || strings.Contains(body.Query, "$status") || strings.Contains(body.Query, "status:") {
						t.Error("unfiltered request contains a status filter")
					}
				} else if string(body.Variables.Status) != fmt.Sprintf("%q", filter) || !strings.Contains(body.Query, "status:$status") || !strings.Contains(body.Query, "$status:MediaListStatus") {
					t.Error("missing or incorrect status filter")
				}
				page := body.Variables.Page
				if page != calls || page < 1 || page > len(want) {
					t.Errorf("unexpected page %d", page)
					w.WriteHeader(400)
					return
				}
				fmt.Fprintf(w, `{"data":{"Page":{"pageInfo":{"hasNextPage":%t},"mediaList":[{"id":%d,"progress":0,"status":%q,"media":{"id":%d}}]}}}`, page < len(want), page, want[page-1], page)
			})
			entries, err := c.List(context.Background(), 42, filter)
			if err != nil || len(entries) != len(want) || calls != len(want) {
				t.Fatalf("entries=%v calls=%d err=%v", entries, calls, err)
			}
			for i, entry := range entries {
				if entry.Status != want[i] {
					t.Fatalf("entry %d: status=%s want=%s", i, entry.Status, want[i])
				}
			}
		})
	}
}

func TestAPIErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		auth   bool
	}{
		{"unauthorized", 401, "secret", true}, {"forbidden", 403, "secret", true},
		{"graphql auth", 200, `{"errors":[{"message":"secret","status":401}]}`, true},
		{"graphql", 200, `{"errors":[{"message":"secret","status":400}]}`, false},
		{"server", 500, "secret", false}, {"malformed", 200, "secret", false},
		{"missing data", 200, `{"data":null}`, false}, {"null viewer", 200, `{"data":{"Viewer":null}}`, false},
		{"oversized", 200, strings.Repeat("x", maxResponse+1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); fmt.Fprint(w, tc.body) })
			_, err := c.Viewer(context.Background())
			if err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatalf("unsafe/missing error: %v", err)
			}
			if tc.auth && !errors.Is(err, ErrAuth) {
				t.Fatal(err)
			}
		})
	}
}

func TestRateLimitAndCancellation(t *testing.T) {
	count := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		count++
		if count == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(429)
			return
		}
		fmt.Fprint(w, `{"data":{"Viewer":{"id":42,"name":"tester"}}}`)
	})
	if _, err := c.Viewer(context.Background()); err != nil || count != 2 {
		t.Fatal(err, count)
	}
	c.next = time.Now().Add(time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Viewer(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	count = 0
	c = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		count++
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(429)
	})
	if _, err := c.Viewer(context.Background()); err == nil || count != 3 {
		t.Fatal(err, count)
	}
}

func TestSearchAndMutation(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var b request[json.RawMessage]
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			t.Error(err)
		}
		if strings.HasPrefix(b.Query, "mutation") {
			fmt.Fprint(w, `{"data":{"SaveMediaListEntry":{"id":7,"progress":4}}}`)
		} else {
			fmt.Fprint(w, `{"data":{"Page":{"pageInfo":{"hasNextPage":false},"media":[{"id":9,"title":{"english":"Frieren"}}]}}}`)
		}
	})
	m, next, err := c.Search(context.Background(), "Frieren", 1)
	if err != nil || len(m) != 1 || next {
		t.Fatal(m, next, err)
	}
	if err := c.SetProgress(context.Background(), 7, 4); err != nil {
		t.Fatal(err)
	}
	if err := c.SetProgress(context.Background(), 7, 5); err == nil {
		t.Fatal("unconfirmed mutation accepted")
	}
}
