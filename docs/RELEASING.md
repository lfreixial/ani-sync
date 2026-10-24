# Preparing a release

The initial public repository is a preview, not a stable release. Do not publish
a stable tag until real OAuth, terminal interaction and playback have been checked
on a supported Linux desktop and all CI checks, including govulncheck, pass.

## Repository setup

Use `main` as the default branch and describe the project as:

> AniList watchlists and confirmed episode progress for ani-cli, written in Go.

Suggested topics: `anilist`, `ani-cli`, `golang`, `linux`, `cli`, `anime`.
Enable private vulnerability reporting and require the CI checks before merging
to main. Do not advertise a successful CI badge before the first successful run.
Repository settings must be checked on GitHub; these files do not enable them.

## Release validation

1. Run `make test race lint build` and `make vuln` using a supported Go release.
2. Verify browser login/state rejection/logout, source matching, cancellation,
   pending-update recovery and progress limits on a local test account. Do not
   include that account's credentials or data in the repository.
3. Review dependency advisories and the documented limitations. Update the
   changelog and verification record with only checks actually completed.
4. Build Linux binaries from the exact reviewed commit. Example for amd64:

   ```sh
   mkdir -p dist
   CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
     go build -trimpath -o dist/ani-sync-linux-amd64 ./cmd/ani-sync
   sha256sum dist/ani-sync-linux-amd64 > dist/SHA256SUMS
   ```

5. Create a tag and GitHub release only when ready. For an initial preview,
   explicitly mark it as a prerelease and include the manual-tracking limitation.
   Attach binaries, checksums and the license; exclude caches, tool installations,
   test data, credentials and local configuration.

CI actions are pinned to full commit IDs verified from the official
[checkout v7.0.1 release](https://github.com/actions/checkout/releases/tag/v7.0.1)
and [setup-go v7.0.0 release](https://github.com/actions/setup-go/releases/tag/v7.0.0).
Dependabot proposes action updates. The official checker is pinned to
[golang/vuln v1.9.0](https://pkg.go.dev/golang.org/x/vuln@v1.9.0/cmd/govulncheck); review and
update that tool version explicitly as needed.
