package tui

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/lfreixial/ani-sync/internal/anilist"
)

func TestPromptsAndEscapes(t *testing.T) {
	var out bytes.Buffer
	u := &UI{In: strings.NewReader("\ny\n4\n"), Err: &out, Out: &out, Interactive: true}
	if ok, err := u.Confirm(context.Background(), "Complete?"); err != nil || ok {
		t.Fatal(ok, err)
	}
	if ok, err := u.Confirm(context.Background(), "Complete?"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if n, err := u.Number(context.Background(), "Episode", 3); err != nil || n != 4 {
		t.Fatal(n, err)
	}
	if s := Clean("a\x1b[2J\n\u202e"); strings.ContainsAny(s, "\x1b\n\u202e") {
		t.Fatal(s)
	}
	u.Interactive = false
	if _, err := u.Prompt(context.Background(), "", "default"); err == nil {
		t.Fatal("noninteractive prompt")
	}
}
func TestPromptEOFAndLimits(t *testing.T) {
	for _, in := range []string{"", strings.Repeat("x", 5000)} {
		u := &UI{In: strings.NewReader(in), Err: io.Discard, Interactive: true}
		if _, err := u.Prompt(context.Background(), "", ""); err == nil {
			t.Fatal("unbounded/EOF input accepted")
		}
	}
}
func TestListRendering(t *testing.T) {
	var b bytes.Buffer
	u := &UI{Out: &b}
	u.Entries([]anilist.Entry{{Progress: 2, Status: anilist.Current, Media: anilist.Media{ID: 4, Title: anilist.Title{Native: "葬送"}}}}, "native")
	if !strings.Contains(b.String(), "2/?") || !strings.Contains(b.String(), "葬送") {
		t.Fatal(b.String())
	}
}

func TestSelectorFallback(t *testing.T) {
	t.Setenv("PATH", "")
	entries := []anilist.Entry{{Media: anilist.Media{ID: 9, Title: anilist.Title{English: "Frieren"}}}, {Media: anilist.Media{ID: 10, Title: anilist.Title{Romaji: "Other"}}}}
	u := &UI{In: strings.NewReader("bad\n10\n"), Out: io.Discard, Err: io.Discard, Interactive: true}
	got, err := u.Select(context.Background(), entries, "english")
	if err != nil || got.Media.ID != 10 {
		t.Fatal(got, err)
	}
	u.Interactive = false
	if _, err := u.Select(context.Background(), entries, "english"); err == nil {
		t.Fatal("noninteractive selection")
	}
}
