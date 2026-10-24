package progress

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/lfreixial/ani-sync/internal/anilist"
	"github.com/lfreixial/ani-sync/internal/config"
)

type fakeAPI struct {
	entry  anilist.Entry
	writes int
	fail   bool
}

func (f *fakeAPI) Entry(context.Context, int, int) (anilist.Entry, error) { return f.entry, nil }
func (f *fakeAPI) SetProgress(_ context.Context, id, n int) error {
	f.writes++
	f.entry.Progress = n
	if f.fail {
		return errors.New("ambiguous network error")
	}
	return nil
}
func service(t *testing.T) (*Service, *fakeAPI) {
	t.Helper()
	s, err := config.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	f := &fakeAPI{entry: anilist.Entry{ID: 3, Progress: 2, Media: anilist.Media{ID: 9, Episodes: 12}}}
	return &Service{s, f, 42}, f
}

func TestSyncDuplicateAndAmbiguousFailure(t *testing.T) {
	s, f := service(t)
	if err := s.Queue(f.entry, 3, false); err != nil {
		t.Fatal(err)
	}
	if err := s.Queue(f.entry, 3, false); err != nil {
		t.Fatal(err)
	}
	f.fail = true
	if _, err := s.Sync(context.Background()); err == nil {
		t.Fatal("expected uncertainty")
	}
	f.fail = false
	if n, err := s.Sync(context.Background()); err != nil || n != 1 || f.writes != 1 {
		t.Fatal(n, err, f.writes)
	}
	pending, _ := s.Pending()
	if len(pending) != 0 {
		t.Fatal(pending)
	}
}
func TestMonotonicAndLimits(t *testing.T) {
	s, f := service(t)
	if err := s.Queue(f.entry, 1, false); err == nil {
		t.Fatal("decrease accepted")
	}
	if err := s.Queue(f.entry, 13, false); err == nil {
		t.Fatal("overrun accepted")
	}
	if err := s.Queue(f.entry, 3, false); err != nil {
		t.Fatal(err)
	}
	f.entry.Progress = 5
	if _, err := s.Sync(context.Background()); err != nil || f.writes != 0 {
		t.Fatal(err, f.writes)
	}
	if err := s.Queue(f.entry, 1, true); err != nil {
		t.Fatal(err)
	}
	f.entry.Progress = 6
	if _, err := s.Sync(context.Background()); err == nil {
		t.Fatal("stale decrease accepted")
	}
}
func TestAccountIsolation(t *testing.T) {
	s, f := service(t)
	if err := s.Queue(f.entry, 3, false); err != nil {
		t.Fatal(err)
	}
	s.UserID = 99
	if n, err := s.Sync(context.Background()); err != nil || n != 0 || f.writes != 0 {
		t.Fatal(n, err)
	}
	p, _ := s.Pending()
	if len(p) != 1 {
		t.Fatal(p)
	}
}
func TestEpisodes(t *testing.T) {
	e := anilist.Entry{Progress: 11, Media: anilist.Media{Episodes: 12}}
	if n, err := Next(e); err != nil || n != 12 {
		t.Fatal(n, err)
	}
	e.Progress = 12
	if _, err := Next(e); err == nil {
		t.Fatal("overrun")
	}
	if ValidateEpisode(0, 12) == nil || ValidateEpisode(13, 12) == nil || ValidateEpisode(1, 0) != nil {
		t.Fatal("episode validation")
	}
}
