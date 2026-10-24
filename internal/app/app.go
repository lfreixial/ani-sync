// Package app composes the CLI without putting domain logic in the terminal UI.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/lfreixial/ani-sync/internal/anilist"
	"github.com/lfreixial/ani-sync/internal/auth"
	"github.com/lfreixial/ani-sync/internal/config"
	"github.com/lfreixial/ani-sync/internal/matching"
	"github.com/lfreixial/ani-sync/internal/playback"
	"github.com/lfreixial/ani-sync/internal/progress"
	"github.com/lfreixial/ani-sync/internal/tui"
)

const Help = `ani-sync — AniList progress with ani-cli playback

Commands:
  login                         Sign in through a local browser
  logout                        Remove local credentials and pending updates
  status                        Verify login and show pending updates
  list [--status STATUS]         Show all lists or one AniList status
  watching | completed | planning
  search [--page N] "title"      Search the AniList catalog
  watch [flags] ["title"]        Select from your AniList list and play
  continue [flags]               Continue the last selected anime
  progress                      Show watchlist progress
  progress set [flags]           Confirm and queue absolute progress
  sync [--discard --yes]         Reconcile pending updates, or discard them
  config                        Display configuration and storage location
  config set client-id ID        Set your public OAuth client ID
  config set title-language english|romaji|native
  doctor                        Check local tools and configuration

Common flags (place flags before positional titles):
  --json                        JSON output for list/search/status/progress
  --non-interactive             Never prompt
Selection: --id MEDIA_ID
Playback:  --episode N --alias "source title" --auto
           --confirm-mapping     Save the alias after successful playback
           --confirm-completed   Explicitly attest completion after playback
           --confirm-gap         Attest earlier unwatched episodes too
Progress:  --episodes N --yes --allow-decrease

Manual confirmation is the default. --auto falls back to manual mode.
Use --id, --episode and a saved mapping or --alias for scripted playback.
ani-cli may still require its own interactive source selection.
`

type App struct {
	UI        *tui.UI
	Store     *config.Store
	Config    config.Config
	NewClient func(string) *anilist.Client
	Play      func(context.Context, string, int, playback.Streams) error
}
type options struct {
	json, nonInteractive, yes, auto, confirmMapping, confirmCompleted, confirmGap, allowDecrease, discard bool
	id, episode, episodes, page                                                                           int
	status, alias                                                                                         string
}

// Run opens configuration only for actual commands, so --help has no side effects.
func Run(ctx context.Context, args []string, in *os.File, out, stderr io.Writer) error {
	ui := &tui.UI{In: in, Out: out, Err: stderr, Interactive: playback.IsTerminal(in)}
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprint(out, Help)
		return nil
	}
	path, err := config.DefaultPath()
	if err != nil {
		return err
	}
	s, err := config.Open(path)
	if err != nil {
		return err
	}
	defer s.Close()
	unlock, err := s.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	cfg, err := config.Load(s)
	if err != nil && args[0] != "logout" {
		return err
	}
	app := &App{UI: ui, Store: s, Config: cfg}
	return app.Execute(ctx, args)
}

func parse(args []string) (string, options, []string, error) {
	var o options
	o.episodes = -1
	o.page = 1
	command := args[0]
	args = args[1:]
	if command == "progress" && len(args) > 0 && args[0] == "set" {
		command = "progress set"
		args = args[1:]
	}
	if command == "config" {
		return command, o, args, nil
	}
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	// Register only meaningful flags to avoid silently ignoring confirmations.
	fs.BoolVar(&o.nonInteractive, "non-interactive", false, "")
	switch command {
	case "list", "watching", "completed", "planning", "search", "status", "progress":
		fs.BoolVar(&o.json, "json", false, "")
	}
	switch command {
	case "watch", "continue":
		fs.IntVar(&o.id, "id", 0, "")
		fs.IntVar(&o.episode, "episode", 0, "")
		fs.StringVar(&o.alias, "alias", "", "")
		fs.BoolVar(&o.auto, "auto", false, "")
		fs.BoolVar(&o.confirmMapping, "confirm-mapping", false, "")
		fs.BoolVar(&o.confirmCompleted, "confirm-completed", false, "")
		fs.BoolVar(&o.confirmGap, "confirm-gap", false, "")
	case "progress set":
		fs.IntVar(&o.id, "id", 0, "")
		fs.IntVar(&o.episodes, "episodes", -1, "")
		fs.BoolVar(&o.yes, "yes", false, "")
		fs.BoolVar(&o.allowDecrease, "allow-decrease", false, "")
	case "sync":
		fs.BoolVar(&o.discard, "discard", false, "")
		fs.BoolVar(&o.yes, "yes", false, "")
	case "list":
		fs.StringVar(&o.status, "status", "", "")
	case "search":
		fs.IntVar(&o.page, "page", 1, "")
	case "login", "logout", "status", "watching", "completed", "planning", "progress", "doctor":
	default:
		return command, o, nil, fmt.Errorf("unknown command %q; run ani-sync --help", tui.Clean(command))
	}
	if err := fs.Parse(args); err != nil {
		return command, o, nil, errors.New("invalid command options; run ani-sync --help (flags go before titles)")
	}
	if o.id < 0 || o.episode < 0 || o.episodes < -1 {
		return command, o, nil, errors.New("IDs and episode counts must not be negative")
	}
	positionals := fs.Args()
	if len(positionals) > 0 && command != "watch" && command != "search" {
		return command, o, nil, errors.New("unexpected positional arguments")
	}
	return command, o, positionals, nil
}

func (a *App) client(ctx context.Context) (*anilist.Client, anilist.User, error) {
	tok, err := auth.Load(a.Store)
	if err != nil {
		return nil, anilist.User{}, err
	}
	c := a.newClient(tok.AccessToken)
	user, err := c.Viewer(ctx)
	if err != nil {
		return nil, user, err
	}
	if tok.UserID != user.ID {
		return nil, user, errors.New("stored credential account mismatch; log in again")
	}
	return c, user, nil
}

func (a *App) newClient(token string) *anilist.Client {
	if a.NewClient != nil {
		return a.NewClient(token)
	}
	return anilist.New(token)
}
func (a *App) output(value any) error { return json.NewEncoder(a.UI.Out).Encode(value) }

func (a *App) Execute(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("command required")
	}
	command, o, positionals, err := parse(args)
	if err != nil {
		return err
	}
	if o.nonInteractive || o.json {
		a.UI.Interactive = false
	}
	switch command {
	case "config":
		return a.configure(positionals)
	case "doctor":
		return a.doctor(ctx)
	case "login":
		if !a.UI.Interactive {
			return errors.New("login requires a terminal and local browser")
		}
		token, err := auth.Login(ctx, a.Config.ClientID, a.UI.Err)
		if err != nil {
			return err
		}
		user, err := a.newClient(token.AccessToken).Viewer(ctx)
		if err != nil {
			return err
		}
		token.UserID = user.ID
		ok, err := a.UI.Confirm(ctx, fmt.Sprintf("Save login for AniList user %s (ID %d)?", tui.Clean(user.Name), user.ID))
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("login not saved")
		}
		if err := auth.Save(a.Store, token); err != nil {
			return err
		}
		fmt.Fprintln(a.UI.Out, "Logged in as", tui.Clean(user.Name))
		return nil
	case "logout":
		if err := auth.Logout(a.Store); err != nil {
			return err
		}
		if err := a.Store.Remove("pending.json"); err != nil {
			return err
		}
		fmt.Fprintln(a.UI.Out, "Local credentials and pending updates removed. Revoke the application in AniList settings to invalidate its token remotely.")
		return nil
	case "search":
		query := strings.Join(positionals, " ")
		if err := matching.ValidateQuery(query); err != nil {
			return err
		}
		media, next, err := a.newClient("").Search(ctx, query, o.page)
		if err != nil {
			return err
		}
		if o.json {
			return a.output(struct {
				Media       []anilist.Media `json:"media"`
				HasNextPage bool            `json:"has_next_page"`
			}{media, next})
		}
		entries := []anilist.Entry{}
		for _, m := range media {
			entries = append(entries, anilist.Entry{Media: m})
		}
		a.UI.Entries(entries, a.Config.TitleLanguage)
		if next {
			fmt.Fprintf(a.UI.Err, "More results: use --page %d\n", o.page+1)
		}
		return nil
	}
	c, user, err := a.client(ctx)
	if err != nil {
		return err
	}
	service := &progress.Service{Store: a.Store, API: c, UserID: user.ID}
	switch command {
	case "status":
		pending, err := service.Pending()
		if err != nil {
			return err
		}
		count := 0
		for _, u := range pending {
			if u.UserID == user.ID {
				count++
			}
		}
		if o.json {
			return a.output(struct {
				User    anilist.User `json:"user"`
				Pending int          `json:"pending"`
			}{user, count})
		}
		fmt.Fprintf(a.UI.Out, "Logged in as %s (ID %d); %d pending updates.\n", tui.Clean(user.Name), user.ID, count)
		return nil
	case "sync":
		if o.discard {
			if !o.yes {
				ok, err := a.UI.Confirm(ctx, "Discard pending updates for this account?")
				if err != nil {
					return err
				}
				if !ok {
					return nil
				}
			}
			if err := service.Discard(); err != nil {
				return err
			}
			fmt.Fprintln(a.UI.Out, "Pending updates discarded.")
			return nil
		}
		return a.sync(ctx, service)
	case "list", "watching", "completed", "planning", "progress":
		status := anilist.Status(strings.ToUpper(o.status))
		switch command {
		case "watching":
			status = anilist.Current
		case "completed":
			status = anilist.Completed
		case "planning":
			status = anilist.Planning
		}
		entries, err := c.List(ctx, user.ID, status)
		if err != nil {
			return err
		}
		if o.json {
			return a.output(entries)
		}
		a.UI.Entries(entries, a.Config.TitleLanguage)
		return nil
	case "progress set":
		entry, err := a.selectEntry(ctx, c, user.ID, o.id, "")
		if err != nil {
			return err
		}
		n := o.episodes
		if n < 0 {
			n, err = a.UI.Number(ctx, "Watched episode count", entry.Progress)
			if err != nil {
				return err
			}
		}
		decrease := o.allowDecrease
		if n < entry.Progress && !decrease {
			decrease, err = a.UI.Confirm(ctx, fmt.Sprintf("Decrease progress from %d to %d?", entry.Progress, n))
			if err != nil {
				return err
			}
			if !decrease {
				return nil
			}
		}
		if !o.yes {
			ok, err := a.UI.Confirm(ctx, fmt.Sprintf("Set %s progress to %d?", tui.Short(entry.Media.Title.Preferred(a.Config.TitleLanguage), 65), n))
			if err != nil {
				return err
			}
			if !ok {
				return nil
			}
		}
		if err := service.Queue(entry, n, decrease); err != nil {
			return err
		}
		return a.sync(ctx, service)
	case "watch", "continue":
		return a.watch(ctx, c, user, service, command, o, strings.Join(positionals, " "))
	}
	return errors.New("unhandled command")
}

func (a *App) sync(ctx context.Context, s *progress.Service) error {
	n, err := s.Sync(ctx)
	if err != nil {
		return fmt.Errorf("sync incomplete; confirmed updates remain queued for 'ani-sync sync': %w", err)
	}
	fmt.Fprintf(a.UI.Out, "Reconciled %d pending update(s).\n", n)
	return nil
}

func (a *App) selectEntry(ctx context.Context, c *anilist.Client, user, id int, query string) (anilist.Entry, error) {
	if id > 0 {
		if query != "" {
			return anilist.Entry{}, errors.New("use either --id or a title")
		}
		return c.Entry(ctx, user, id)
	}
	entries, err := c.List(ctx, user, "")
	if err != nil {
		return anilist.Entry{}, err
	}
	return a.UI.Select(ctx, matching.Filter(entries, query), a.Config.TitleLanguage)
}

type lastPlayed struct {
	UserID  int `json:"user_id"`
	MediaID int `json:"media_id"`
}

func (a *App) watch(ctx context.Context, c *anilist.Client, user anilist.User, s *progress.Service, command string, o options, query string) error {
	if command == "continue" {
		if o.id != 0 {
			return errors.New("continue uses the last anime; use watch --id to select one")
		}
		var last lastPlayed
		if err := a.Store.Read("last.json", &last); err != nil {
			return errors.New("no last anime available; use ani-sync watch")
		}
		if last.UserID != user.ID || last.MediaID <= 0 {
			return errors.New("last anime belongs to a different account; use ani-sync watch")
		}
		o.id = last.MediaID
	}
	pending, err := s.Pending()
	if err != nil {
		return err
	}
	for _, u := range pending {
		if u.UserID == user.ID {
			return errors.New("confirmed progress is pending; run ani-sync sync before playback")
		}
	}
	entry, err := a.selectEntry(ctx, c, user.ID, o.id, query)
	if err != nil {
		return err
	}
	episode := o.episode
	if episode == 0 {
		next, err := progress.Next(entry)
		if err != nil {
			return err
		}
		if a.UI.Interactive {
			episode, err = a.UI.Number(ctx, "Episode to play", next)
			if err != nil {
				return err
			}
		} else {
			episode = next
		}
	}
	if err := progress.ValidateEpisode(episode, entry.Media.Episodes); err != nil {
		return err
	}
	aliases, err := matching.Load(a.Store)
	if err != nil {
		return err
	}
	alias := o.alias
	if alias == "" {
		alias = aliases.Get(entry.Media.ID)
	}
	if alias == "" {
		if !a.UI.Interactive {
			return errors.New("scripted playback requires a saved title mapping or --alias")
		}
		alias, err = a.UI.Prompt(ctx, "Source search title (verify season/version in ani-cli)", entry.Media.Title.Preferred("romaji"))
		if err != nil {
			return err
		}
	}
	if err := matching.ValidateQuery(alias); err != nil {
		return err
	}
	if o.auto {
		fmt.Fprintln(a.UI.Err, playback.AutoFallback)
	}
	fmt.Fprintf(a.UI.Err, "Playing %s, episode %d. Select the matching season/version in ani-cli.\n", tui.Short(entry.Media.Title.Preferred(a.Config.TitleLanguage), 65), episode)
	play := a.Play
	if play == nil {
		play = playback.Launch
	}
	err = play(ctx, alias, episode, playback.Streams{In: a.UI.In, Out: a.UI.Out, Err: a.UI.Err})
	if err != nil {
		return fmt.Errorf("playback ended unsuccessfully; progress unchanged: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := a.Store.Write("last.json", lastPlayed{user.ID, entry.Media.ID}); err != nil {
		return err
	}
	confirmed := o.confirmCompleted
	if !confirmed && a.UI.Interactive {
		confirmed, err = a.UI.Confirm(ctx, fmt.Sprintf("Did you finish episode %d of %s, and was it the correct season/version?", episode, tui.Short(entry.Media.Title.Preferred(a.Config.TitleLanguage), 65)))
		if err != nil {
			return err
		}
	}
	mappingConfirmed := o.confirmMapping
	if !mappingConfirmed && confirmed && a.UI.Interactive && aliases.Get(entry.Media.ID) != alias {
		mappingConfirmed, err = a.UI.Confirm(ctx, "Save this source search title for future playback?")
		if err != nil {
			return err
		}
	}
	if mappingConfirmed {
		if err := aliases.Save(a.Store, entry.Media.ID, alias); err != nil {
			return err
		}
	}
	if !confirmed {
		fmt.Fprintln(a.UI.Out, "Progress unchanged. Use progress set when completion is confirmed.")
		return nil
	}
	if episode <= entry.Progress {
		fmt.Fprintln(a.UI.Out, "Episode already recorded; progress unchanged.")
		return nil
	}
	if episode > entry.Progress+1 && !o.confirmGap {
		ok, err := a.UI.Confirm(ctx, fmt.Sprintf("AniList progress includes all earlier episodes. Have you also completed episodes %d–%d?", entry.Progress+1, episode-1))
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(a.UI.Out, "Progress unchanged.")
			return nil
		}
	}
	if err := s.Queue(entry, episode, false); err != nil {
		return err
	}
	return a.sync(ctx, s)
}

func (a *App) configure(args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(a.UI.Out, "Storage:", tui.Clean(a.Store.Path))
		return a.output(a.Config)
	}
	if len(args) != 3 || args[0] != "set" {
		return errors.New("usage: config set client-id ID | title-language english|romaji|native")
	}
	c := a.Config
	switch args[1] {
	case "client-id":
		c.ClientID = args[2]
	case "title-language":
		c.TitleLanguage = args[2]
	default:
		return errors.New("unknown configuration key")
	}
	if err := c.Validate(); err != nil {
		return err
	}
	if err := a.Store.Write("config.json", c); err != nil {
		return err
	}
	fmt.Fprintln(a.UI.Out, "Configuration saved.")
	return nil
}

func (a *App) doctor(ctx context.Context) error {
	fmt.Fprintf(a.UI.Out, "Go runtime: %s\nStorage: %s (0700; files checked when opened)\n", runtime.Version(), tui.Clean(a.Store.Path))
	failed := false
	for _, name := range []string{"ani-cli", "mpv", "fzf", "curl", "sed", "grep"} {
		path, err := playback.TrustedPath(name)
		if err == nil && name == "ani-cli" {
			err = playback.Check(ctx, path)
		}
		if err != nil {
			failed = true
			fmt.Fprintf(a.UI.Out, "%s: %s\n", name, tui.Clean(err.Error()))
		} else {
			fmt.Fprintf(a.UI.Out, "%s: %s\n", name, tui.Clean(path))
		}
	}
	if a.Config.ClientID == "" {
		fmt.Fprintln(a.UI.Out, "OAuth client ID: not configured")
	} else {
		fmt.Fprintln(a.UI.Out, "OAuth client ID:", a.Config.ClientID)
	}
	fmt.Fprintln(a.UI.Out, "OAuth redirect:", auth.RedirectURI)
	fmt.Fprintln(a.UI.Out, playback.AutoFallback)
	if _, err := auth.Load(a.Store); err != nil {
		fmt.Fprintln(a.UI.Out, "Local login:", tui.Clean(err.Error()))
	} else {
		fmt.Fprintln(a.UI.Out, "Local login: stored (not checked online)")
	}
	if failed {
		return errors.New("one or more playback dependencies failed validation")
	}
	return nil
}
