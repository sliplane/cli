# Releasing Sliplane CLI

GoReleaser builds macOS and Linux binaries for amd64 and arm64. Native Windows
installation is not included yet; Windows users can use WSL. The CLI does not
check for updates or update itself.

## Validate locally

Use the Go version from `go.mod` or a compatible newer version, and GoReleaser
2.15.1 (the version pinned in both GitHub Actions workflows).

```sh
make check
make release-snapshot
SLIPLANE_TEST_RELEASE_DIR="$PWD/dist" go test ./scripts -run '^TestReleaseInstall$' -count=1
```

Snapshots build every supported target and create archives and checksums under
`dist/` without publishing. The installer smoke test installs the host's actual
archive into a temporary directory and checks its embedded version and commit.
Normal installer tests run offline as part of `make check`.

CI runs vet and race tests on Linux and macOS. It also builds all release targets
and runs the release installer smoke test on Linux. Cross-compilation verifies
that every target builds; it does not execute every target's binary.

## Create a release

1. Merge the release changes into `main` and ensure CI passes.
2. Choose a new semantic version. Use `v0.1.0` for a stable release or
   `v0.1.0-rc.1` for a prerelease. Never reuse a published tag.
3. From the clean, up-to-date checkout, create and push that tag:

   ```sh
   git tag -a v0.1.0 -m 'Release v0.1.0'
   git push origin v0.1.0
   ```

4. The Release workflow reruns CI, then creates a **draft** GitHub Release using
   the repository's `GITHUB_TOKEN`. No personal access token is needed.
5. Review its notes and artifacts. Test the downloaded native archive with
   `sliplane version` and `sliplane --help`. Publish the draft when ready. Keep
   release candidates marked as prereleases; only a stable release should be
   selected as GitHub's latest release.
6. Check the public installer command in the README after publishing.

GitHub Actions must be enabled and allowed to grant the release job
`contents: write`. Downloading release assets without authentication requires a
public repository. The first public installation will work only after the first
release is published. Draft assets are not available to anonymous installers.

## Artifact contract

Each release includes:

- `sliplane_VERSION_darwin_amd64.tar.gz`
- `sliplane_VERSION_darwin_arm64.tar.gz`
- `sliplane_VERSION_linux_amd64.tar.gz`
- `sliplane_VERSION_linux_arm64.tar.gz`
- `checksums.txt`
- `install.sh`

Archives contain `sliplane`, `LICENSE`, and `NOTICE`. `VERSION` excludes the tag's
leading `v`. The binary reports this version and the full Git commit hash.
