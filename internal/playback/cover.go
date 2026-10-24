package playback

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const maxCoverBytes = 5 << 20

var (
	coverClient = &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	coverHost   = func(host string) bool { return host == "anilist.co" || strings.HasSuffix(host, ".anilist.co") }
	coverDir    = func() (string, error) {
		dir, err := os.UserCacheDir()
		return filepath.Join(dir, "ani-sync", "covers"), err
	}
)

// Cover returns a local copy of an AniList cover image, downloading it once into
// the user cache. It returns "" on any failure; playback works without art.
func Cover(ctx context.Context, mediaID int, coverURL string) string {
	path, err := cover(ctx, mediaID, coverURL)
	if err != nil {
		return ""
	}
	return path
}

func cover(ctx context.Context, mediaID int, coverURL string) (string, error) {
	u, err := url.Parse(coverURL)
	if err != nil || u.Scheme != "https" || !coverHost(u.Hostname()) || mediaID <= 0 {
		return "", errors.New("cover URL is not an AniList https image")
	}
	dir, err := coverDir()
	if err != nil {
		return "", err
	}
	if strings.ContainsAny(dir, unsafePathChars) {
		return "", errors.New("cache path is unsafe for ani-cli player flags")
	}
	if _, err := os.Lstat(dir); err == nil && !privateDir(dir) {
		return "", errors.New("cover cache directory is not private")
	}
	for _, ext := range []string{".jpg", ".png", ".webp"} {
		if p := filepath.Join(dir, fmt.Sprintf("%d%s", mediaID, ext)); regularFile(p) {
			return p, nil
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	resp, err := coverClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	ext := map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp"}[strings.TrimSpace(strings.SplitN(resp.Header.Get("Content-Type"), ";", 2)[0])]
	if resp.StatusCode != http.StatusOK || ext == "" {
		return "", errors.New("cover download failed")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxCoverBytes+1))
	if err != nil || len(data) == 0 || len(data) > maxCoverBytes {
		return "", errors.New("cover image is empty or too large")
	}
	if !isImage(data, ext) {
		return "", errors.New("cover content does not match its image type")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	if !privateDir(dir) {
		return "", errors.New("cover cache directory is not private")
	}
	tmp, err := os.CreateTemp(dir, ".cover-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	path := filepath.Join(dir, fmt.Sprintf("%d%s", mediaID, ext))
	return path, os.Rename(tmp.Name(), path)
}

// isImage checks the file signature, so a mislabelled response is never handed to mpv.
func isImage(data []byte, ext string) bool {
	switch ext {
	case ".jpg":
		return bytes.HasPrefix(data, []byte{0xff, 0xd8, 0xff})
	case ".png":
		return bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n"))
	case ".webp":
		return len(data) >= 12 && bytes.HasPrefix(data, []byte("RIFF")) && string(data[8:12]) == "WEBP"
	}
	return false
}

// privateDir reports whether dir is a real directory that other users cannot write to.
func privateDir(dir string) bool {
	st, err := os.Lstat(dir)
	return err == nil && st.IsDir() && st.Mode().Perm()&0022 == 0
}

func regularFile(path string) bool {
	st, err := os.Lstat(path)
	return err == nil && st.Mode().IsRegular()
}
