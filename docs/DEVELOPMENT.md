# Development and verification

Use the current stable Go release. This checkout was developed with Go 1.27.3
on Linux amd64. The application uses only Go's standard library; consequently
`go.mod` has no require entries and no `go.sum` is needed or generated. Runtime
external programs are the existing ani-cli/mpv/fzf installation, not Go modules.

```sh
make build
make test
make race
make lint
```

The Makefile keeps caches and test temporary files under `.cache/`, which is
ignored. `make install` and `make uninstall` act only on the user binary by
default. Set `PREFIX`, `BINDIR` or `DESTDIR` for packaging.

The equivalent Go checks are:

```sh
go test ./...
go test -race ./...
go vet ./...
govulncheck ./...
```

Tests use a real net/http server over net.Pipe connections so they also run in
sandboxes that prohibit network sockets. OAuth handlers use synthetic callbacks.
The fake subprocess is the compiled Go test binary, not an executed shell script.
No tests contact AniList, acquire credentials or visit streaming sites.

## Vulnerability checker

Do not install tools implicitly. With the user's permission, install the official
checker into this checkout only:

```sh
mkdir -p .tools .cache/go-build .cache/go-mod .cache/tmp
GOBIN="$PWD/.tools" GOCACHE="$PWD/.cache/go-build" \
  GOMODCACHE="$PWD/.cache/go-mod" GOTMPDIR="$PWD/.cache/tmp" \
  go install golang.org/x/vuln/cmd/govulncheck@v1.9.0
make vuln
```

The checker and its dependencies are development tools; they are not linked into
ani-sync. It needs network access for installation and the vulnerability database.
Run it again whenever the Go toolchain changes. No third-party Go runtime
dependency does **not** mean the standard library or playback dependencies have
no vulnerabilities.

CI and these instructions pin govulncheck v1.9.0, whose
[module dependencies](https://github.com/golang/vuln/blob/v1.9.0/go.mod) include
x/tools v0.52.0. The original v1.1.4 pin included x/tools v0.29.0; the first
reported CI scan on Go 1.27.3 crashed inside that older SSA analyzer with
`panic: unexpected expr: *ast.KeyValueExpr`. This was a scanner failure and
produced no completed vulnerability assessment. Updating the scanner preserves
the full `govulncheck ./...` check and its failure exit status. CI records the
scanner build information so future toolchain mismatches are visible in logs.

The updated scanner could not be downloaded in the development sandbox, so its
successful installation and scan must still be verified by the next CI run.

## Development phases

1. Official OAuth, GraphQL pagination/rate limits, ani-cli manual and installed
   source inspected. Decisions recorded in ARCHITECTURE.md.
2. Storage, authentication, typed API and durable progress implemented and tested.
3. Foreground ani-cli launching, explicit arguments, mappings and cancellation
   implemented with subprocess tests.
4. fzf selection, plain prompt fallback, language display and command composition
   implemented with workflow tests.
5. Automatic tracking disabled at the documented source/episode identity boundary.
   The fallback is tested; there are no IPC claims or unconnected tracker stubs.
6. Security review, build, unit/integration tests, race and vet checks performed.
   See the checked verification record below.

## Verification record

Final checks on 2026-10-24:

| Check | Result |
| --- | --- |
| `go test ./...` (`make test`) | Passed |
| `go test -race ./...` (`make race`) | Passed |
| `go vet ./...` (`make lint`) | Passed |
| Go formatting check | Passed |
| `go build -buildvcs=false -trimpath -o ani-sync ./cmd/ani-sync` | Passed |
| `ani-sync --help`, configuration write and `doctor` | Passed with project-local XDG paths |
| Fake ani-cli launch, unsupported flags, arguments and child environment | Passed |
| Live fzf PTY smoke | Could not run: sandbox denied controlling-terminal access |
| PTY terminal restoration with fake children | Passed after terminal fix: normal exit, failure and cancellation |
| `govulncheck ./...` | Could not run: authorized tool installation blocked by network sandbox |

The dependency review (`go list -m all`) reports only the main module. No
third-party Go dependencies are linked. Known vulnerabilities in the standard
library or external playback tools could not be assessed with govulncheck; no
claim of vulnerability-free code is made. The local review covered file modes,
symlink/hardlink handling, credential redaction, OAuth state isolation, terminal
control injection, environment forwarding, process cancellation, and progress
reconciliation. This is an implementation review, not an independent audit.

Live login and real streaming playback are intentionally untested. This sandbox
denies TCP listeners and external DNS sockets. The authorized local govulncheck
installation failed when reaching proxy.golang.org because socket creation was
denied. Consequently vulnerability scanning could not run, and no clean
vulnerability report is claimed. No software was installed outside the project.

The supplied workspace's read-only `.git` placeholder is not a valid repository.
The direct build therefore requires `-buildvcs=false` here (as used by Makefile).
The standard build command works in a regular checkout with valid Git metadata.
