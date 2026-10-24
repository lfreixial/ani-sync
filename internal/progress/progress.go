// Package progress reconciles durable, account-bound absolute progress updates.
package progress

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/lfreixial/ani-sync/internal/anilist"
	"github.com/lfreixial/ani-sync/internal/config"
)

const queueFile = "pending.json"

type API interface {
	Entry(context.Context, int, int) (anilist.Entry, error)
	SetProgress(context.Context, int, int) error
}
type Update struct {
	UserID        int  `json:"user_id"`
	MediaID       int  `json:"media_id"`
	Target        int  `json:"target"`
	Baseline      int  `json:"baseline"`
	AllowDecrease bool `json:"allow_decrease"`
}
type Service struct {
	Store  *config.Store
	API    API
	UserID int
}

func Next(entry anilist.Entry) (int, error) {
	if entry.Progress < 0 || entry.Progress >= 2147483647 {
		return 0, errors.New("invalid existing progress")
	}
	next := entry.Progress + 1
	if entry.Media.Episodes > 0 && next > entry.Media.Episodes {
		return 0, errors.New("all known episodes are watched; choose an episode explicitly to rewatch")
	}
	return next, nil
}
func ValidateEpisode(episode, total int) error {
	if episode < 1 || episode > 2147483647 {
		return errors.New("episode must be a positive 32-bit integer")
	}
	if total > 0 && episode > total {
		return fmt.Errorf("episode exceeds AniList total of %d", total)
	}
	return nil
}
func validateTarget(target, total int) error {
	if target < 0 || target > 2147483647 {
		return errors.New("progress must be a nonnegative 32-bit integer")
	}
	if total > 0 && target > total {
		return fmt.Errorf("progress exceeds AniList total of %d", total)
	}
	return nil
}

func (s *Service) Pending() ([]Update, error) {
	updates := []Update{}
	err := s.Store.Read(queueFile, &updates)
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	if err != nil {
		return nil, err
	}
	for _, u := range updates {
		if u.UserID <= 0 || u.MediaID <= 0 || u.Baseline < 0 || validateTarget(u.Target, 0) != nil {
			return nil, errors.New("invalid pending update")
		}
	}
	return updates, nil
}

// Queue must only be called after explicit user confirmation of the target.
func (s *Service) Queue(entry anilist.Entry, target int, allowDecrease bool) error {
	if s.UserID <= 0 || entry.Media.ID <= 0 {
		return errors.New("invalid progress account or media")
	}
	if err := validateTarget(target, entry.Media.Episodes); err != nil {
		return err
	}
	if target < entry.Progress && !allowDecrease {
		return errors.New("decreasing progress requires explicit confirmation")
	}
	updates, err := s.Pending()
	if err != nil {
		return err
	}
	for _, u := range updates {
		if u.UserID == s.UserID && u.MediaID == entry.Media.ID {
			if u.Target == target {
				return nil
			}
			return errors.New("a different update is pending for this anime; sync it or discard it first")
		}
	}
	if target == entry.Progress {
		return nil
	}
	updates = append(updates, Update{s.UserID, entry.Media.ID, target, entry.Progress, allowDecrease})
	return s.Store.Write(queueFile, updates)
}

func (s *Service) Discard() error {
	updates, err := s.Pending()
	if err != nil {
		return err
	}
	kept := []Update{}
	for _, u := range updates {
		if u.UserID != s.UserID {
			kept = append(kept, u)
		}
	}
	return s.Store.Write(queueFile, kept)
}

// Sync never retries an uncertain mutation blindly: a subsequent call rereads.
func (s *Service) Sync(ctx context.Context) (int, error) {
	updates, err := s.Pending()
	if err != nil {
		return 0, err
	}
	count := 0
	for i := 0; i < len(updates); {
		u := updates[i]
		if u.UserID != s.UserID {
			i++
			continue
		}
		entry, err := s.API.Entry(ctx, s.UserID, u.MediaID)
		if err != nil {
			return count, err
		}
		if err := validateTarget(u.Target, entry.Media.Episodes); err != nil {
			return count, err
		}
		write := u.Target > entry.Progress
		if u.AllowDecrease && u.Target != entry.Progress {
			if entry.Progress != u.Baseline {
				return count, errors.New("remote progress changed after decrease confirmation; discard the pending update and confirm again")
			}
			write = true
		}
		if write {
			if err := s.API.SetProgress(ctx, entry.ID, u.Target); err != nil {
				return count, err
			}
		}
		updates = append(updates[:i], updates[i+1:]...)
		if err := s.Store.Write(queueFile, updates); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}
