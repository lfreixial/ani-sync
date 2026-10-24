# Changelog

## Unreleased — initial public preview

- AniList implicit OAuth with verified state and private local token storage.
- Typed GraphQL watchlists, catalog search, pagination and bounded rate retries.
- Interactive selection and ani-cli foreground playback with confirmed aliases.
- Explicit completion confirmation and durable, account-bound progress updates.
- JSON output, configuration commands, diagnostics and installation targets.
- Mock HTTP and subprocess tests, race checks and GitHub CI configuration.

Automatic tracking is disabled with a manual fallback because the documented
ani-cli interface does not provide a reliable source/episode identity handoff.
Real OAuth and streaming playback still need opt-in validation by a user.
No stable release or completed vulnerability scan is claimed.
