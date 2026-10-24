# Changelog

## Unreleased — initial public preview

- AniList implicit OAuth with verified state and private local token storage.
- Authorization URLs use AniList's registered redirect instead of an explicit
  override, matching the documented implicit flow.
- Typed GraphQL watchlists, catalog search, pagination and bounded rate retries.
- Bare `search` prompts for an anime title in a terminal; scripts receive a
  usage example when the title is missing.
- Unfiltered watchlists omit the status argument instead of sending null, fixing
  empty selections in `watch` when status-specific lists contain anime.
- Interactive selection and ani-cli foreground playback with confirmed aliases.
- Use a clean mpv configuration and the FFmpeg demuxer to play HLS episodes as
  continuous media instead of a playlist of short segments.
- Restore terminal ownership after child processes without SIGTTOU suspension;
  cover success, failure and cancellation with pseudo-terminal regression tests.
- Explicit completion confirmation and durable, account-bound progress updates.
- JSON output, configuration commands, diagnostics and installation targets.
- Mock HTTP and subprocess tests, race checks and GitHub CI configuration.

Automatic tracking is disabled with a manual fallback because the documented
ani-cli interface does not provide a reliable source/episode identity handoff.
Real OAuth and streaming playback still need opt-in validation by a user.
No stable release or completed vulnerability scan is claimed.
