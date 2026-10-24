// Package auth implements AniList's public-client implicit grant.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/lfreixial/ani-sync/internal/config"
)

const RedirectURI = "http://127.0.0.1:8765/callback"
const credentialFile = "credentials.json"

type Token struct {
	AccessToken string    `json:"access_token"`
	ExpiresAt   time.Time `json:"expires_at"`
	UserID      int       `json:"user_id"`
}

func (t Token) Validate() error {
	if len(t.AccessToken) < 1 || len(t.AccessToken) > 8192 || strings.ContainsAny(t.AccessToken, "\r\n\t ") {
		return errors.New("invalid OAuth token")
	}
	if t.ExpiresAt.IsZero() || !time.Now().Before(t.ExpiresAt) {
		return errors.New("OAuth token expired; run ani-sync login")
	}
	return nil
}
func Load(s *config.Store) (Token, error) {
	var t Token
	if err := s.Read(credentialFile, &t); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return t, errors.New("not logged in; run ani-sync login")
		}
		return t, err
	}
	return t, t.Validate()
}
func Save(s *config.Store, t Token) error {
	if err := t.Validate(); err != nil {
		return err
	}
	if t.UserID <= 0 {
		return errors.New("token must be verified before storage")
	}
	return s.Write(credentialFile, t)
}
func Logout(s *config.Store) error { return s.Remove(credentialFile) }

type receiver struct {
	state, nonce, host string
	result             chan Token
}

func newReceiver(host string) (*receiver, error) {
	var b [64]byte
	if _, err := rand.Read(b[:]); err != nil {
		return nil, err
	}
	return &receiver{base64.RawURLEncoding.EncodeToString(b[:32]), base64.RawURLEncoding.EncodeToString(b[32:]), host, make(chan Token, 1)}, nil
}

func (r *receiver) authorizationURL(clientID string) string {
	// AniList's implicit flow uses the redirect registered in the application
	// settings. Match its documented request instead of sending redirect_uri.
	q := url.Values{"client_id": {clientID}, "response_type": {"token"}, "state": {r.state}}
	return "https://anilist.co/api/v2/oauth/authorize?" + q.Encode()
}

// The fragment is removed before any requests. No third-party content is loaded.
const callbackHTML = `<!doctype html><meta charset="utf-8"><title>ani-sync login</title>
<p id="status">Completing login…</p><script nonce="%s">
const p = new URLSearchParams(location.hash.slice(1));
history.replaceState(null, '', '/callback');
fetch('/token', {method:'POST', headers:{'Content-Type':'application/json'},
body:JSON.stringify({access_token:p.get('access_token'),state:p.get('state'),
token_type:p.get('token_type'),expires_in:p.get('expires_in')})})
.then(r=>{document.getElementById('status').textContent=r.ok?'Return to ani-sync to finish login.':'Login rejected. Return to ani-sync and retry.';})
.catch(()=>{document.getElementById('status').textContent='Login could not complete. Return to ani-sync.';});
</script>`

func (r *receiver) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'nonce-"+r.nonce+"'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
	if req.Host != r.host {
		http.Error(w, "invalid host", http.StatusForbidden)
		return
	}
	switch {
	case req.URL.Path == "/callback" && req.Method == http.MethodGet:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, callbackHTML, r.nonce)
	case req.URL.Path == "/token" && req.Method == http.MethodPost:
		if req.Header.Get("Origin") != "http://"+r.host || req.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "invalid origin", http.StatusForbidden)
			return
		}
		req.Body = http.MaxBytesReader(w, req.Body, 16384)
		var payload struct {
			AccessToken string `json:"access_token"`
			State       string `json:"state"`
			TokenType   string `json:"token_type"`
			ExpiresIn   string `json:"expires_in"`
		}
		dec := json.NewDecoder(req.Body)
		if dec.Decode(&payload) != nil {
			http.Error(w, "invalid callback", 400)
			return
		}
		if dec.Decode(new(any)) != io.EOF {
			http.Error(w, "invalid callback", 400)
			return
		}
		if subtle.ConstantTimeCompare([]byte(payload.State), []byte(r.state)) != 1 || !strings.EqualFold(payload.TokenType, "bearer") {
			http.Error(w, "invalid OAuth state or type", 400)
			return
		}
		seconds, err := strconv.ParseInt(payload.ExpiresIn, 10, 64)
		if err != nil || seconds <= 0 || seconds > 366*24*3600 {
			http.Error(w, "invalid token expiry", 400)
			return
		}
		token := Token{AccessToken: payload.AccessToken, ExpiresAt: time.Now().Add(time.Duration(seconds) * time.Second)}
		if token.Validate() != nil {
			http.Error(w, "invalid token", 400)
			return
		}
		select {
		case r.result <- token:
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "already received", 409)
		}
	default:
		http.NotFound(w, req)
	}
}

// Login prints a browser URL; it never spawns a browser or prompts for credentials.
func Login(ctx context.Context, clientID string, out io.Writer) (Token, error) {
	n, err := strconv.Atoi(clientID)
	if err != nil || n <= 0 {
		return Token{}, errors.New("set your AniList client ID with ani-sync config set client-id ID")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	listener, err := net.Listen("tcp4", "127.0.0.1:8765")
	if err != nil {
		return Token{}, errors.New("cannot bind OAuth callback at 127.0.0.1:8765; close the application using this port")
	}
	receiver, err := newReceiver("127.0.0.1:8765")
	if err != nil {
		listener.Close()
		return Token{}, err
	}
	server := &http.Server{Handler: receiver, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 10 * time.Second, MaxHeaderBytes: 8192, ErrorLog: log.New(io.Discard, "", 0)}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	defer func() {
		// Let the browser receive the callback response before closing the server.
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if server.Shutdown(shutdown) != nil {
			_ = server.Close()
		}
	}()
	fmt.Fprintln(out, "Open this URL in your local browser (expires in 5 minutes):\n"+receiver.authorizationURL(clientID))
	select {
	case token := <-receiver.result:
		return token, nil
	case <-ctx.Done():
		return Token{}, fmt.Errorf("OAuth login cancelled or timed out: %w", ctx.Err())
	case <-done:
		return Token{}, errors.New("OAuth callback server stopped")
	}
}
