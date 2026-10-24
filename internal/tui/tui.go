// Package tui contains terminal presentation, independent of synchronization.
package tui

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"unicode"

	"github.com/lfreixial/ani-sync/internal/anilist"
	"github.com/lfreixial/ani-sync/internal/playback"
)

// Clean prevents remote titles from injecting terminal controls or bidi marks.
func Clean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, s)
}
func Short(s string, max int) string {
	r := []rune(Clean(s))
	if len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return string(r)
}

type UI struct {
	In          io.Reader
	Out, Err    io.Writer
	Interactive bool
	scanner     *bufio.Scanner
}

func (u *UI) Prompt(ctx context.Context, label, defaultValue string) (string, error) {
	if !u.Interactive {
		return "", errors.New("interactive input required; provide explicit command flags")
	}
	fmt.Fprint(u.Err, Clean(label))
	if defaultValue != "" {
		fmt.Fprintf(u.Err, " [%s]", Clean(defaultValue))
	}
	fmt.Fprint(u.Err, ": ")
	if u.scanner == nil {
		u.scanner = bufio.NewScanner(u.In)
		u.scanner.Buffer(make([]byte, 1024), 4096)
	}
	type answer struct {
		line string
		err  error
	}
	result := make(chan answer, 1)
	go func() {
		if !u.scanner.Scan() {
			err := u.scanner.Err()
			if err == nil {
				err = io.EOF
			}
			result <- answer{err: err}
			return
		}
		result <- answer{line: strings.TrimSpace(u.scanner.Text())}
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case a := <-result:
		if a.err != nil {
			return "", a.err
		}
		if a.line == "" {
			return defaultValue, nil
		}
		return a.line, nil
	}
}
func (u *UI) Confirm(ctx context.Context, label string) (bool, error) {
	s, err := u.Prompt(ctx, label+" [y/N]", "")
	return strings.EqualFold(s, "y") || strings.EqualFold(s, "yes"), err
}
func (u *UI) Number(ctx context.Context, label string, def int) (int, error) {
	s, err := u.Prompt(ctx, label, strconv.Itoa(def))
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, errors.New("enter a whole episode number")
	}
	return n, nil
}

func Row(e anilist.Entry, language string) string {
	total := "?"
	if e.Media.Episodes > 0 {
		total = strconv.Itoa(e.Media.Episodes)
	}
	return fmt.Sprintf("%s  %d/%s  %s", Short(e.Media.Title.Preferred(language), 65), e.Progress, total, e.Status)
}
func (u *UI) Entries(entries []anilist.Entry, language string) {
	w := tabwriter.NewWriter(u.Out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tTITLE\tWATCHED\tSTATUS")
	for _, e := range entries {
		total := "?"
		if e.Media.Episodes > 0 {
			total = strconv.Itoa(e.Media.Episodes)
		}
		fmt.Fprintf(w, "%d\t%s\t%d/%s\t%s\n", e.Media.ID, Short(e.Media.Title.Preferred(language), 65), e.Progress, total, e.Status)
	}
	w.Flush()
}
func (u *UI) Select(ctx context.Context, entries []anilist.Entry, language string) (anilist.Entry, error) {
	if len(entries) == 0 {
		return anilist.Entry{}, errors.New("no anime matched your list; add it on AniList or try another title")
	}
	if !u.Interactive {
		return anilist.Entry{}, errors.New("use --id MEDIA_ID with --non-interactive to select an anime")
	}
	if len(entries) == 1 {
		return entries[0], nil
	}
	if fzf, err := playback.TrustedPath("fzf"); err == nil {
		var input strings.Builder
		for _, e := range entries {
			fmt.Fprintf(&input, "%d\t%s\n", e.Media.ID, Row(e, language))
		}
		selected, err := playback.Capture(ctx, fzf, []string{"--no-multi", "--delimiter=\t", "--with-nth=2..", "--layout=reverse", "--height=80%", "--border", "--prompt=Anime > ", "--header=Type to search · arrows to move · Enter to select · Esc to cancel"}, playback.Environment(), strings.NewReader(input.String()), u.Err)
		if err != nil {
			return anilist.Entry{}, fmt.Errorf("selection cancelled or failed: %w", err)
		}
		id, err := strconv.Atoi(strings.SplitN(strings.TrimSpace(selected), "\t", 2)[0])
		if err != nil {
			return anilist.Entry{}, errors.New("invalid selector response")
		}
		for _, e := range entries {
			if e.Media.ID == id {
				return e, nil
			}
		}
		return anilist.Entry{}, errors.New("selector returned an unknown anime")
	}
	// A minimal fallback works over plain terminal connections without fzf.
	for {
		u.Entries(entries, language)
		value, err := u.Prompt(ctx, "Choose an AniList ID (or q to cancel)", "")
		if err != nil {
			return anilist.Entry{}, err
		}
		if value == "q" {
			return anilist.Entry{}, context.Canceled
		}
		id, err := strconv.Atoi(value)
		if err == nil {
			for _, e := range entries {
				if e.Media.ID == id {
					return e, nil
				}
			}
		}
		fmt.Fprintln(u.Err, "Choose an ID from the displayed list.")
	}
}
