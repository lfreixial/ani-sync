# Architecture and interface research

Research date: 2026-10-24. Development environment: Linux amd64, Go 1.27.3,
ani-cli 5.1.0, mpv and fzf installed. No existing application code.

Official references:

- https://go.dev/dl/ — installed Go matches the current stable release.
- https://docs.anilist.co/guide/auth/ — implicit and authorization-code grants;
  no refresh tokens or scopes. No documented PKCE or device authorization grant.
- https://docs.anilist.co/guide/auth/implicit — browser fragment token response.
- https://docs.anilist.co/guide/graphql/pagination — use `hasNextPage`, not totals.
- https://docs.anilist.co/guide/rate-limiting — obey Retry-After and reset headers;
  the documented temporary limit is lower than the usual limit.
- https://docs.anilist.co/reference/object/page — 50 entries/page, depth 5000.
- https://docs.anilist.co/reference/mutation — SaveMediaListEntry.
- https://github.com/pystardust/ani-cli/blob/master/ani-cli.1 — episode selection,
  --no-detach, --exit-after-play, ANI_CLI_PLAYER and ANI_CLI_PLAYER_FLAGS.
- https://mpv.io/manual/stable/ — JSON IPC and its local security limitations.

## Boundaries

`config` owns descriptor-relative, symlink-resistant local storage and locking.
`auth` owns the short-lived loopback OAuth receiver. `anilist` owns typed GraphQL
and bounded HTTP. `progress` owns monotonic reconciliation and a durable queue.
`matching` owns aliases and title search. `playback` owns validated process
launches. `tui` owns rendering, prompts and the optional fzf selector. `app`
composes these packages. No external Go dependencies or background service.

Use the official implicit grant for a public desktop client. Generate random
state and require its exact return; if the provider omits state, fail closed.
The docs do not explicitly promise state echo, so live interoperability remains
unverified until a user performs login. Do not silently relax CSRF checks.
No user account is accessed during development.

Progress changes are absolute values, queued before network mutation and checked
against a fresh server read. Replays are idempotent; local processes serialize
with flock. AniList has no documented conditional mutation, so simultaneous
edits from other clients cannot be made transactional. Decreases require a
separate explicit flag or prompt and a matching baseline on replay.

## Playback and automatic tracking limitation

The documented foreground flags allow waiting for one requested episode, but
ani-cli does not expose a structured source ID or episode confirmation to the
caller. It performs source selection internally. Its current implementation
constructs mpv's display title, but that string is not a documented machine
interface. A stored search alias is not proof of the actual selected source.

This release therefore deliberately disables automatic progress updates,
including when --auto is requested, and explains the manual fallback. It does
not create an IPC socket or claim completion from exit status. Reliable mpv
position alone would not prove that the correct AniList episode was watched.
Implementing automatic mode requires a documented identity handoff in addition
to seek-aware coverage tracking and private IPC. No dormant partial tracker is
shipped. IPC-specific tests are consequently not applicable to this release.

The UI uses fzf (already an ani-cli dependency) through explicit arguments,
with its configuration/environment hooks disabled, plus a numbered fallback.
This provides search, arrow-key navigation and resizing without a second TUI
framework. Machine-readable commands never invoke the selector.
