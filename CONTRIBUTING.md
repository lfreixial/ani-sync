# Contributing

ani-sync targets Linux and the current stable Go release. Keep the core small,
explicit and independent of terminal presentation. Open an issue before adding a
large dependency, changing authentication, or changing synchronization semantics.

## Local development

```sh
make test
make race
make lint
make build
```

Tests must use synthetic credentials and mocked services. They must never require
an AniList account, a streaming website, or installed ani-cli/mpv. Fake playback
programs are compiled test binaries. See [development notes](docs/DEVELOPMENT.md)
for the separately installed vulnerability checker and sandbox limitations.

Use `gofmt` and add tests for meaningful behavior changes. Keep new error messages
free of credentials and remote response bodies. Use explicit subprocess arguments,
context cancellation, bounded network reads and the private storage package.

Progress updates must remain explicitly confirmed and account-bound. Never treat
process termination as completion. Keep authentication state checks and decrease
confirmations intact. New upstream interfaces need official documentation and
tests, not assumptions based on an output string.

## Pull requests

Explain the user-visible problem, the resulting behavior and the checks you ran.
Keep unrelated changes separate. Small changes do not need elaborate abstractions.
Be respectful and specific in reviews; discuss the code and the evidence.

GitHub CI runs tests, race detection, vet, formatting, builds and govulncheck. It
has read-only repository permissions and does not receive AniList credentials.
Its first remote execution remains pending until the repository is published.

Contributions are licensed under the project's MIT license. Use the private
reporting guidance in [SECURITY.md](SECURITY.md) for security issues.
