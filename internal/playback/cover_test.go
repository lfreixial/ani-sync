package playback

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func withCoverServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, string) {
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)
	dir := filepath.Join(t.TempDir(), "covers")
	oldClient, oldHost, oldDir := coverClient, coverHost, coverDir
	coverClient = srv.Client()
	coverHost = func(host string) bool { return host == "127.0.0.1" }
	coverDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { coverClient, coverHost, coverDir = oldClient, oldHost, oldDir })
	return srv, dir
}

func TestCoverDownloadsOnce(t *testing.T) {
	hits := 0
	srv, dir := withCoverServer(t, func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("\xff\xd8\xff\xe0jpeg"))
	})
	for range 2 {
		if got := Cover(context.Background(), 5114, srv.URL+"/cover.jpg"); got != filepath.Join(dir, "5114.jpg") {
			t.Fatalf("got %q", got)
		}
	}
	if hits != 1 {
		t.Fatalf("want one download, got %d", hits)
	}
}

func TestCoverRejectsUnsafeSources(t *testing.T) {
	srv, _ := withCoverServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>"))
	})
	for _, u := range []string{srv.URL + "/not-an-image", "http://127.0.0.1/cover.jpg", "https://example.com/cover.jpg", "", "file:///etc/passwd"} {
		if got := Cover(context.Background(), 1, u); got != "" {
			t.Fatalf("%q accepted as %q", u, got)
		}
	}
	if got := Cover(context.Background(), 0, srv.URL+"/x.jpg"); got != "" {
		t.Fatalf("invalid media id accepted: %q", got)
	}
}

func TestCoverRejectsMislabelledContent(t *testing.T) {
	srv, _ := withCoverServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("<html>not a png</html>"))
	})
	if got := Cover(context.Background(), 7, srv.URL+"/cover.png"); got != "" {
		t.Fatalf("mislabelled content accepted as %q", got)
	}
}

func TestCoverRejectsSharedCacheDirectory(t *testing.T) {
	srv, dir := withCoverServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("\xff\xd8\xff\xe0jpeg"))
	})
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0777); err != nil {
		t.Fatal(err)
	}
	if got := Cover(context.Background(), 8, srv.URL+"/cover.jpg"); got != "" {
		t.Fatalf("world-writable cache accepted: %q", got)
	}
	// A cover planted in a shared cache is not reused either.
	if err := os.WriteFile(filepath.Join(dir, "9.jpg"), []byte("\xff\xd8\xff"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := Cover(context.Background(), 9, srv.URL+"/cover.jpg"); got != "" {
		t.Fatalf("cached cover in shared directory reused: %q", got)
	}
}

func TestIsImage(t *testing.T) {
	cases := []struct {
		ext, data string
		want      bool
	}{
		{".jpg", "\xff\xd8\xff\xe0", true},
		{".png", "\x89PNG\r\n\x1a\n", true},
		{".webp", "RIFF\x00\x00\x00\x00WEBPVP8 ", true},
		{".jpg", "\x89PNG\r\n\x1a\n", false},
		{".webp", "RIFF\x00\x00\x00\x00AVI ", false},
		{".gif", "GIF89a", false},
	}
	for _, c := range cases {
		if got := isImage([]byte(c.data), c.ext); got != c.want {
			t.Errorf("isImage(%q, %q) = %v", c.data, c.ext, got)
		}
	}
}

func TestLaunchPassesCoverToMPV(t *testing.T) {
	dir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ani-cli", "mpv"} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0700); err != nil {
			t.Fatal(err)
		}
	}
	plugin := filepath.Join(dir, "mpris.so")
	cover := filepath.Join(dir, "5114.jpg")
	for _, f := range []string{plugin, cover} {
		if err := os.WriteFile(f, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	withMPRISPlugins(t, []string{plugin})
	flags := func(cover string) string {
		var out bytes.Buffer
		if err := Launch(context.Background(), "Sousou", 3, cover, Streams{Out: &out, Err: io.Discard}); err != nil {
			t.Fatal(err)
		}
		var report struct{ Env []string }
		if err := json.Unmarshal(out.Bytes(), &report); err != nil {
			t.Fatal(err)
		}
		for _, e := range report.Env {
			if v, ok := strings.CutPrefix(e, "ANI_CLI_PLAYER_FLAGS="); ok {
				return v
			}
		}
		t.Fatal("no player flags")
		return ""
	}
	base := "--no-config --demuxer=lavf --terminal=yes --script=" + plugin
	if got := flags(cover); got != base+" --vid=1 --cover-art-files="+cover {
		t.Fatalf("with cover: %q", got)
	}
	colon := filepath.Join(dir, "a:b.jpg")
	if err := os.WriteFile(colon, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", filepath.Join(dir, "missing.jpg"), filepath.Join(dir, "a b.jpg"), colon} {
		if got := flags(bad); got != base {
			t.Fatalf("cover %q: %q", bad, got)
		}
	}
}
