# Security policy and implementation notes

Do not include credentials, OAuth redirect fragments, or private list data in
public issues. Use your repository host's private vulnerability reporting feature
where enabled, or contact its maintainers privately before publishing a report.
This initial project has no dedicated security mailbox or audited security claim.

## Threat model

The application protects against accidental disclosure, untrusted API text,
argument/option injection, unsafe local file destinations, and implicit progress
changes. It trusts the current Unix user, the OS, the installed Go toolchain,
AniList over verified HTTPS, and the installed playback programs. Root or another
process running as the same user can access tokens and control playback. Plaintext
token storage is permission-protected, not encrypted. Deletion is unlinking, not
guaranteed erasure from storage devices, snapshots, backups or browser history.

## OAuth and HTTP

- The supported AniList implicit flow avoids a distributable client secret.
  A random 256-bit state is required in the response, even though AniList's guide
  does not explicitly document state echo. Missing state fails closed.
- The receiver binds only `127.0.0.1:8765`, lasts at most five minutes, checks Host
  and exact Origin, and accepts a size-limited JSON POST. It serves no third-party
  scripts, clears the fragment and sets a restrictive CSP and no-store policy.
  Its CSP nonce is independent of OAuth state. The page does not reveal state.
- Callback tokens are verified with Viewer, and the user confirms the returned
  account identity before storage. Passwords and pasted tokens are never requested.
- AniList grants broad access with no scopes and no refresh tokens. `logout`
  removes local credentials and queued updates; remote revocation is a separate
  action in AniList settings. No undocumented revocation endpoint is assumed.
- API traffic uses verified HTTPS, 25-second request timeouts, a 4 MiB response
  limit and no redirects. Request variables are typed, encoded JSON, not query
  interpolation. Pagination uses hasNextPage, de-duplicates IDs and has an
  explicit API-depth limit. Search is explicitly paged.
- Error bodies, GraphQL error messages, tokens and transport error strings are
  not echoed. Authentication failure and rate limiting receive local messages.
  Progress mutations are not blindly retried after ambiguous network failures.

## Local storage

Configuration paths must be absolute. Directory components are traversed with
Linux `openat(O_DIRECTORY|O_NOFOLLOW|O_CLOEXEC)`. The final directory must belong
to the current user and is set to 0700 using its open descriptor. Symlink paths
are deliberately unsupported, including symlinked XDG configuration directories.

Files are opened relative to that descriptor with O_NOFOLLOW and O_NONBLOCK;
regular-file type, owner, single link and mode 0600 are checked before use.
O_NONBLOCK prevents FIFO destinations from hanging a command. JSON reads are
bounded. Files with unsafe existing permissions are rejected rather than read.

Writes use random exclusive 0600 temporary files, fsync, descriptor-relative
rename and directory fsync. They never truncate an existing destination in place.
The destination is checked, and rename does not follow a destination symlink.
An advisory flock serializes commands from this application. A crash can leave
a private `.tmp-*` file; it is not automatically imported or followed.

Hostile same-user processes can still alter files, replace executables between
validation and exec, ignore locks or modify directory structure. The application
does not claim to sandbox other applications with the same UID.

## Processes and terminal

- os/exec receives explicit slices; no shell, eval, remote install script, or
  shell-based browser opener is launched by ani-sync. ani-cli itself is an
  installed upstream shell script executed via its shebang; its implementation
  and subprocesses are a separate trust boundary.
- Source titles cannot start with '-' and cannot contain controls, bidi formatting
  or invalid UTF-8. They are one argument. The upstream parser does not document
  a '--' terminator, so ani-sync does not invent one.
- Executables resolve to absolute regular executable files with trusted ownership
  and no group/world write access along their path. System-owned sticky temporary
  ancestors are allowed. Rootless containers may have a nonzero root filesystem
  owner; that owner is treated as the OS owner.
- Child environments are allowlisted for terminal, desktop, home/XDG paths and
  validated PATH directories. No token, ANI_CLI override, FZF_DEFAULT_OPTS,
  loader hook or shell startup hook is inherited. ani-sync supplies its own
  mpv/menu settings and disables ani-cli's syslog playback logging.
- mpv runs with fixed `--no-config --demuxer=lavf --terminal=yes` options.
  Default system/user mpv configuration and scripts are bypassed; media is
  handled by the FFmpeg demuxer and player diagnostics stay visible.
  An installed mpv-mpris plugin is explicitly loaded from supported system paths
  after resolving symlinks and checking ownership, permissions and path safety.
  It exposes playback metadata and controls on the local session D-Bus.
  With the plugin loaded, the anime's AniList cover is passed as artwork
  (`--cover-art-files`, with `--vid=1` so the episode stays the shown track).
  The cover is fetched without credentials, over https from `anilist.co` hosts
  only, with no redirects, a 10 s timeout and a 5 MiB limit, and only `image/*`
  responses are accepted. It is cached in `$XDG_CACHE_HOME/ani-sync/covers/`, in
  a directory created with mode 0700. Any failure plays without artwork.
- Children inherit ordinary stdin/out/err only, not token or lock descriptors.
  Ctrl-C and cancellation prevent progress updates. Context cancellation kills
  the process group; foreground terminal ownership is restored after subprocess
  completion. Normal player exit still requires explicit completion confirmation.
- Remote title text is stripped of terminal control/format characters before
  display. fzf receives plain rows, with no command bindings from the environment.

## Progress and automatic mode

Only user-confirmed progress is queued. Queue records contain the verified account
ID; another account's updates are not sent. Before each mutation, remote progress
is fetched again. Already-applied updates are removed without another mutation.
Ordinary updates never lower the fresh value. Explicit decreases require the
confirmed baseline to remain unchanged, or they stop for a new confirmation.
Known episode limits are enforced. Unknown totals cannot be bounded by AniList;
inputs still must be valid nonnegative GraphQL integers.

AniList has no documented compare-and-swap mutation. Another client editing the
same anime between the fresh read and mutation can race with ani-sync. Avoid
simultaneous edits; local locking only serializes ani-sync processes. No stronger
cross-client transactional guarantee is claimed. List status and repeat count are
preserved, not inferred from episode count.

Automatic mode is disabled. An exit code or playback percentage cannot establish
episode identity through the documented ani-cli interface. No mpv IPC listener
or background tracking component exists in this release. An eventual automatic
implementation must verify source/episode identity, use private session IPC,
measure unique watched coverage (not maximum position), handle seeks/restarts,
and fail back to explicit confirmation on ambiguity. It should default to 90%.

## Network connections

| Component | Destination / reason |
| --- | --- |
| ani-sync API client | `https://graphql.anilist.co` for Viewer, lists, catalog and confirmed progress |
| User's browser | `https://anilist.co/api/v2/oauth/authorize` and AniList's normal website resources for login |
| Local browser | `http://127.0.0.1:8765/callback` and `/token`; only while login runs |
| Go HTTP transport | May use HTTP_PROXY / HTTPS_PROXY / NO_PROXY according to Go's environment proxy support |
| Installed ani-cli | Its current upstream source, stream hosts, subtitles and source-related APIs; domains change with upstream |
| Installed mpv | Selected media/subtitle/CDN hosts and any services configured by the user in mpv |
| Build of ani-sync | No module downloads; only the installed standard library is used |
| Optional govulncheck install | Go module proxy/checksum services, normally proxy.golang.org and sum.golang.org |
| Optional vulnerability scan | Go vulnerability database, normally vuln.go.dev |

At research time the inspected ani-cli version used `hianime.at` and streaming
hosts discovered at runtime. ani-sync does not scrape, contact or pin those
services itself. User mpv configuration and installed ani-cli have their own
network/file behavior. This inventory does not claim to constrain them.

There is no telemetry, analytics, update check, remote script installer or
background service. Live OAuth and streaming were intentionally not exercised
during development. See DEVELOPMENT.md for the actual verification results.
