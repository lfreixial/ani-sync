package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lfreixial/ani-sync/internal/config"
)

func TestOAuthCallback(t *testing.T) {
	for _, tc := range []struct {
		name, state, origin, tokenType string
		want                           int
	}{
		{"valid", "correct", "http://127.0.0.1:8765", "Bearer", 204},
		{"wrong state", "wrong", "http://127.0.0.1:8765", "Bearer", 400},
		{"missing state", "", "http://127.0.0.1:8765", "Bearer", 400},
		{"cross origin", "correct", "https://evil.example", "Bearer", 403},
		{"wrong type", "correct", "http://127.0.0.1:8765", "Other", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := newReceiver("127.0.0.1:8765")
			if err != nil {
				t.Fatal(err)
			}
			state := tc.state
			if state == "correct" {
				state = r.state
			}
			b, _ := json.Marshal(map[string]string{"access_token": "never-log-this", "state": state, "expires_in": "3600", "token_type": tc.tokenType})
			req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8765/token", strings.NewReader(string(b)))
			req.Header.Set("Origin", tc.origin)
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
			if strings.Contains(w.Body.String(), "never-log-this") {
				t.Fatal("token leaked")
			}
			if tc.want == 204 {
				select {
				case tok := <-r.result:
					if tok.AccessToken != "never-log-this" {
						t.Fatal("wrong token")
					}
				default:
					t.Fatal("missing token")
				}
			}
		})
	}
}

func TestOAuthURLAndPage(t *testing.T) {
	r, _ := newReceiver("127.0.0.1:8765")
	u, err := url.Parse(r.authorizationURL("123"))
	if err != nil {
		t.Fatal(err)
	}
	if u.Query().Get("state") != r.state || u.Query().Get("response_type") != "token" || u.Query().Get("redirect_uri") != RedirectURI {
		t.Fatal(u)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", RedirectURI, nil))
	if strings.Contains(w.Body.String(), r.state) || strings.Contains(w.Header().Get("Content-Security-Policy"), r.state) {
		t.Fatal("callback exposes OAuth state")
	}
	if !strings.Contains(w.Body.String(), "history.replaceState") || !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatal("callback lacks protections")
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "http://evil.example/callback", nil))
	if w.Code != 403 {
		t.Fatal("host accepted")
	}
}

func TestTokenLifecycle(t *testing.T) {
	s, err := config.Open(filepath.Join(t.TempDir(), "ani-sync"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	tok := Token{"secret", time.Now().Add(time.Hour), 42}
	if err := Save(s, tok); err != nil {
		t.Fatal(err)
	}
	got, err := Load(s)
	if err != nil || got.AccessToken != tok.AccessToken {
		t.Fatal(got, err)
	}
	if err := Logout(s); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(s); err == nil {
		t.Fatal("logout failed")
	}
	tok.ExpiresAt = time.Now().Add(-time.Hour)
	if err := Save(s, tok); err == nil {
		t.Fatal("expired token accepted")
	}
}
