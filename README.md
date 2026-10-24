# ani-sync

AniList watchlists and confirmed episode progress for the existing **ani-cli**
player. Built for CachyOS / Arch Linux in Go, with no external Go dependencies,
telemetry, or background service.

**Status: initial public preview.** Real OAuth and streaming playback still need
user validation. See the [verification record](docs/DEVELOPMENT.md#verification-record)
for checks that passed and checks blocked in the development sandbox.

**Manual confirmation is the default and the only enabled tracking mode.**
After successful playback, ani-sync asks whether you finished the requested
episode of the correct season/version. Exiting a player does not prove completion.

## Build and install

Requires Go 1.27 or newer (developed with stable Go 1.27.3). Playback requires
your installed ani-cli with `--episode`, `--no-detach`, and `--exit-after-play`,
plus mpv and ani-cli's normal dependencies, including fzf. ani-cli 5.1.0 was
inspected during development. Install those separately using your normal package
manager; ani-sync never installs or updates them.

```sh
go build -trimpath -o ani-sync ./cmd/ani-sync
install -Dm755 ani-sync ~/.local/bin/ani-sync
ani-sync doctor
```

Alternatively, `make build` and `make install` install into `~/.local/bin`.
Ensure that directory is on your PATH. No root access is required. `make build`
disables VCS stamping, which also supports exported workspaces with incomplete
Git metadata. In such a workspace, use `go build -buildvcs=false -trimpath -o
ani-sync ./cmd/ani-sync` for the direct equivalent.

## OAuth setup

1. Open [AniList developer settings](https://anilist.co/settings/developer) in
   your browser and create an application.
2. Set its redirect URL **exactly** to `http://127.0.0.1:8765/callback`.
   AniList uses this saved setting; ani-sync does not send a redirect override
   in the implicit authorization URL.
3. Copy its public **client ID**, then run:

   ```sh
   ani-sync config set client-id 12345
   ani-sync login
   ```

4. Open the displayed authorization URL in a browser on the same machine,
   authorize with AniList, return to the terminal and confirm the account name.

Do not supply a password, client secret, or access token to ani-sync. It uses
AniList's [official implicit grant](https://docs.anilist.co/guide/auth/implicit),
receiving the token through a short-lived loopback callback. The browser clears
the URL fragment before delivering it to the local receiver. OAuth state must
match; a missing state is rejected. The callback expires after five minutes.

AniList [does not offer scopes or refresh tokens](https://docs.anilist.co/guide/auth/).
Tokens have broad account access; sign in again when a token expires. Login
was tested with synthetic callbacks, not a real account. See
[security details](docs/SECURITY.md) for the threat model and remaining limits.

## Everyday use

```sh
ani-sync watching
ani-sync list
ani-sync list --status PAUSED
ani-sync search "Frieren"
ani-sync watch "Frieren"
ani-sync continue
ani-sync progress
ani-sync sync
```

`watch` selects an anime **already on your AniList list**. Search results do not
add an entry; add it through AniList first. The fzf picker supports typing to
search, arrows, Enter and Escape. Without fzf, ani-sync offers a numbered-ID
fallback, though ani-cli still needs its own menu dependency. English, Romaji,
native titles and synonyms are searched locally. Display language is configurable.

The suggested episode is current progress + 1. You can edit it before launch.
ani-cli then asks you to select the actual source result. Verify the season,
version and episode numbering. After completion, you can save the source search
alias. A saved alias is only a query, **not a stable source ID**; source selection
is deliberately never replaced with a guessed result index.

Playback uses your installed mpv with `--no-config --demuxer=lavf --terminal=yes`.
This bypasses custom mpv configuration and scripts, keeps diagnostics visible,
and handles HLS through the FFmpeg demuxer so an episode plays as continuous
media instead of hundreds of short playlist segments. Your mpv configuration
files are not modified. Close the player to return to the completion prompt.
If mpv-mpris is already installed in a supported system location and passes
ownership and permission checks, ani-sync loads it explicitly for desktop media
controls. Other mpv scripts remain disabled. The anime's AniList cover is then
shown as the artwork; covers are cached in `~/.cache/ani-sync/covers/`.

Confirmed changes are queued durably before sending to AniList. `sync` retries
pending changes after rereading server progress. It skips already-applied updates
and never lowers progress implicitly. A gap requires confirmation that earlier
episodes were also watched. Known episode totals are enforced. Rewatching an
earlier episode leaves progress unchanged; repeat counters and list status are
not changed automatically.

## Command reference

Place flags **before** positional titles. `ani-sync --help` prints a short reference.

| Command | Purpose / relevant flags |
| --- | --- |
| `login` | Browser OAuth; requires configured client ID and interactive terminal |
| `logout` | Remove local token and all queued updates; retain aliases/configuration |
| `status [--json]` | Verify current account and count its pending updates |
| `list [--status STATUS] [--json]` | All six statuses, or CURRENT / PLANNING / COMPLETED / PAUSED / DROPPED / REPEATING |
| `watching`, `completed`, `planning` | Filtered lists; support `--json` |
| `search [--page N] [--json] ["title"]` | Public AniList catalog; prompts for a missing title in a terminal; JSON/scripts require a title; 50 results/page; no login required |
| `watch [--id ID] [--episode N] [--alias TITLE] ["title"]` | Select from your list and play one episode |
| `continue [--episode N] [--alias TITLE]` | Last successfully played anime for this account; suggests next unwatched episode |
| `progress [--json]` | Current server watchlist progress |
| `progress set [--id ID] [--episodes N] [--yes] [--allow-decrease]` | Set absolute watched count; prompts when omitted |
| `sync` | Reconcile this account's queued confirmations |
| `sync --discard --yes` | Discard this account's pending changes |
| `config` | Display non-secret settings and storage directory |
| `config set client-id ID` | Configure the public AniList application ID |
| `config set title-language english\|romaji\|native` | Preferred title with fallbacks |
| `doctor` | Inspect local tools, OAuth setup and stored-token expiry; no API calls |

`--non-interactive` suppresses ani-sync prompts. `--json` also suppresses prompts
and writes JSON to stdout; errors go to stderr. Exit codes: 0 success, 1 failure,
130 context cancellation. Invalid/expired login is an error, not a logged-in status.

Examples for scripts:

```sh
ani-sync list --json
ani-sync search --page 2 --json "Frieren"
ani-sync progress set --id 154587 --episodes 3 --yes --non-interactive
# A decrease needs both explicit target confirmation and --allow-decrease:
ani-sync progress set --id 154587 --episodes 2 --allow-decrease --yes --non-interactive
```

Playback additionally accepts `--confirm-mapping`, `--confirm-completed` and
`--confirm-gap`. These are explicit attestations by the caller; use them only
when the corresponding fact is known. Without `--confirm-completed`, scripted
playback never changes progress. A saved mapping or `--alias` and `--id` are
required when selection cannot be interactive. ani-cli itself may still prompt
for source selection, so **fully unattended playback is not supported**.

## Automatic tracking limitation

`watch --auto` and `continue --auto` print a warning and use manual confirmation.
No mpv IPC socket is created in this release. ani-cli documents forwarding player
flags, but does not provide a structured, documented handoff identifying the
selected source and episode. Position and duration alone cannot verify that the
requested AniList episode was watched. Parsing ani-cli's generated display title
would rely on an undocumented interface.

The intended future threshold is 90% verified coverage, with seeks, restarts,
missing duration and episode identity handled conservatively. No partial tracker
is shipped or advertised as working. See [research and architecture](docs/ARCHITECTURE.md).

## Data and connections

All ani-sync data lives under `$XDG_CONFIG_HOME/ani-sync/`, defaulting to
`~/.config/ani-sync/`. The directory is `0700`; files are `0600`.

| File | Contents |
| --- | --- |
| `config.json` | Client ID and title language; see [example](config.example.json) |
| `credentials.json` | Plaintext OAuth token, expiry and verified account ID |
| `mappings.json` | Confirmed AniList media ID → source search query mappings |
| `pending.json` | Account-bound confirmed progress updates awaiting reconciliation |
| `last.json` | Last successfully played media ID and account ID |
| `lock` | Advisory command lock; no credentials |
| `.tmp-*` | Possible remnants of interrupted atomic writes; may contain private data |

Configuration is normally changed using `config set`, which writes safely. If
editing manually, preserve `0600` permissions. Symlinked config paths and files,
hardlinked files and overly permissive files are rejected. The ani-sync directory
is tightened to `0700`. Parent directories are not chmodded.

ani-sync contacts only `https://graphql.anilist.co`. Your browser contacts
`https://anilist.co` for OAuth and its usual website dependencies. The callback
is local HTTP on `127.0.0.1:8765` during login only. Go's HTTP transport may use
your explicitly configured HTTPS proxy environment. ani-cli/mpv perform their
own streaming connections and maintain their own caches/history independently;
their endpoints can change. No AniList credential is placed in child arguments
or environment. See the [complete network inventory](docs/SECURITY.md#network-connections).

## Troubleshooting

- **Another command is running:** finish or cancel the first command in its
  original terminal. If `jobs -l` shows a suspended ani-sync job, resume it with
  `fg %N` (replace N with its job number), then press Ctrl+C to cancel. Rebuild
  and reinstall if using a version that suspends after the anime selector.
  Do not delete the lock file: the lock is released when the process exits.
- **Not logged in / unauthorized:** run `login`; check client ID and exact redirect
  URI. A denied, expired or revoked token is never refreshed silently.
- **Browser reports `unsupported_grant_type`:** rebuild and reinstall the current
  version, cancel the previous login, then run `login` again and open the complete
  new URL. It must use `/api/v2/oauth/authorize` with `response_type=token` and no
  `redirect_uri` override. Check the saved redirect in AniList developer settings.
- **Callback fails:** use a local browser, allow loopback, and free port 8765.
  State, origin and host must match. Never work around failure by pasting a token.
- **No list match:** try Romaji/native titles or `watch --id ID`; add missing anime
  on AniList. `search` queries the catalog, while `watch` searches your lists.
- **Wrong source/season:** decline completion, then run with `--alias "correct
  query"`. Confirm and save the replacement mapping after correct playback.
- **Pending update blocks watch:** run `sync`. If a stale explicit decrease
  conflicts with a newer remote edit, discard it and confirm a new target.
- **Rate limited:** the client waits for Retry-After/reset, with at most three
  attempts and a two-minute maximum individual wait. Longer limits return an
  error. Retry later; pending confirmations remain saved.
- **Player fails:** run `doctor`, then diagnose your installed ani-cli/mpv.
  Unsupported flags are rejected before playback. Progress remains unchanged.
- **Executable rejected:** PATH entries must be absolute and trusted. Executables
  and parent directories must not be writable by other users (system sticky
  temporary parents are allowed). Use trusted package-manager installations.
- **Private file rejected:** check ownership, symlinks, hardlinks and mode. Do not
  copy the credential contents into an issue. Correct permissions to `0600` only
  after verifying that the file is yours and is not a link.
- **Another command is running:** one command holds a local advisory lock,
  including during playback. End that command first; do not delete the lock file.
- **Unavailable network or loopback in a sandbox:** run the built executable in a
  normal local terminal. Tests do not require network access or real credentials.

## Development and uninstall

```sh
make test
make race
make lint
make vuln                 # requires separately authorized/installed govulncheck
make uninstall            # removes ~/.local/bin/ani-sync only
```

To remove private data, first run `ani-sync logout`, revoke the application's
access in AniList settings, then delete the `ani-sync` configuration directory
listed by `ani-sync config`. Do not delete the entire XDG configuration directory.
Aliases and history from ani-cli/mpv are separate and are not removed.

See [development and verification](docs/DEVELOPMENT.md) and
[security policy](docs/SECURITY.md). Licensed under [MIT](LICENSE).

Contributions are welcome; start with [CONTRIBUTING.md](CONTRIBUTING.md).
Changes are tracked in [CHANGELOG.md](CHANGELOG.md), and maintainers can use the
[release guide](docs/RELEASING.md). This project is independent of AniList and
ani-cli and is not endorsed by either project.
