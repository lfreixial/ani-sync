package playback

import (
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/lfreixial/ani-sync/internal/matching"
	"github.com/lfreixial/ani-sync/internal/progress"
)

const AutoFallback = "Automatic sync is unavailable: ani-cli has no documented source/episode identity handoff. Manual completion confirmation is required."

func Arguments(query string, episode int) ([]string, error) {
	if err := matching.ValidateQuery(query); err != nil {
		return nil, err
	}
	if err := progress.ValidateEpisode(episode, 0); err != nil {
		return nil, err
	}
	// ani-cli does not document a '--' separator. A title is one non-option argument.
	return []string{"--episode", strconv.Itoa(episode), "--no-detach", "--exit-after-play", query}, nil
}

func Check(ctx context.Context, path string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	help, err := Capture(ctx, path, []string{"--help"}, Environment(), nil, io.Discard)
	if err != nil {
		return err
	}
	for _, flag := range []string{"--episode", "--no-detach", "--exit-after-play"} {
		if !strings.Contains(help, flag) {
			return errors.New("installed ani-cli lacks required foreground/episode flags; update it through your package manager")
		}
	}
	return nil
}

func Launch(ctx context.Context, query string, episode int, streams Streams) error {
	args, err := Arguments(query, episode)
	if err != nil {
		return err
	}
	ani, err := TrustedPath("ani-cli")
	if err != nil {
		return err
	}
	if err := Check(ctx, ani); err != nil {
		return err
	}
	mpv, err := TrustedPath("mpv")
	if err != nil {
		return err
	}
	if strings.ContainsAny(mpv, " \t\r\n*?[") {
		return errors.New("mpv path contains characters unsafe for ani-cli player selection")
	}
	env := append(Environment(), "ANI_CLI_PLAYER="+mpv, "ANI_CLI_PLAYER_FLAGS=", "ANI_CLI_MENU=fzf", "ANI_CLI_MENU_FLAGS=", "ANI_CLI_LOG=0", "ANI_CLI_SKIP_INTRO=0", "ANI_CLI_DEFAULT_SOURCE=search")
	return Run(ctx, ani, args, env, streams)
}
