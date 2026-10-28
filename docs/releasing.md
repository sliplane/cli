# Releasing Sliplane CLI

GoReleaser builds macOS and Linux binaries for amd64 and arm64. Native Windows
installation is not included yet; Windows users can use WSL. Release builds
update themselves; see [Automatic updates](#automatic-updates).

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

CI runs vet and race tests on Linux and macOS on Namespace runners. Pushes to
`main` also build all release targets and run the release installer smoke test
on Linux. Cross-compilation verifies that every target builds; it does not
execute every target's binary.

## How releases are protected

Publishing a release takes a maintainer's YubiKey, its PIN, and a touch. A
compromised GitHub account, token, or workflow is not enough.

1. The release tag is signed with a YubiKey. The Release workflow builds only
   tags on `main` signed by a key in `.github/allowed_signers` on `main`.
2. CI creates a draft release, with build provenance attestations once the
   repository is public.
3. A maintainer rebuilds the release locally, checks that it is byte-identical
   to the draft, and signs `checksums.txt` with a YubiKey.
4. `install.sh` and `sliplane update` refuse releases without a valid
   signature from one of the keys they embed.

## Enroll a YubiKey

macOS's own `ssh-keygen` cannot use YubiKeys. Install Homebrew's OpenSSH, but
keep it off `PATH`: its `ssh` rejects macOS options such as `UseKeychain`.

```sh
brew install openssh && brew unlink openssh
```

Set a FIDO2 PIN (`ykman fido access change-pin`). Then, with only that YubiKey
connected, enroll and test it under a unique name:

```sh
make enroll-key KEY=yubikey-backup
make signing-check KEY=yubikey-backup
```

This creates `~/.ssh/sliplane-release-NAME` and adds the key to
`.github/allowed_signers`, `scripts/install.sh`, and
`internal/update/allowed_signers`. Merge the change to `main` before using the
key. Installed CLIs only trust the keys built into them, so sign with a new key
only after a release that includes it has been out for a while. On another computer, restore the key file with
`/opt/homebrew/opt/openssh/bin/ssh-keygen -K`.

## Remove a key

A lost YubiKey is still protected by its PIN, which locks after 8 wrong
attempts. Remove its line from all three files in a pull request. Newer installers
then reject releases signed with that key, and immutable releases cannot be
re-signed, so publish a new release if needed.

## Create a release

1. Merge the release changes into `main` and ensure CI passes.
2. Choose a new semantic version. Use `v0.1.0` for a stable release or
   `v0.1.0-rc.1` for a prerelease. Never reuse a published tag.
3. From a clean checkout of the latest `main`, create and push a signed tag.
   Add `KEY=NAME` to use a YubiKey other than `yubikey-main`:

   ```sh
   make release-tag TAG=v0.1.0
   ```

4. The Release workflow verifies the tag, reruns CI, waits for approval in the
   `release` environment, and creates a **draft** release.
5. Review the draft's notes, then rebuild, compare, sign, and publish it. This
   needs GoReleaser 2.15.1; Go is pinned to the version in `go.mod`.

   ```sh
   make sign-release TAG=v0.1.0
   ```

   If the rebuild does not match the draft, do not publish. Investigate the
   release workflow first.
6. Keep release candidates marked as prereleases; only a stable release should
   be selected as GitHub's latest release. Check the public installer command in
   the README after publishing.

GitHub Actions must be enabled and allowed to grant the release job
`contents: write`. Downloading release assets without authentication requires a
public repository. The first public installation will work only after the first
release is published. Draft assets are not available to anonymous installers.

## Required GitHub settings

These live outside the repository; review them when maintainers change.

- **Organization:** require two-factor authentication, and have maintainers use
  security keys or passkeys only.
- **Releases:** enable immutable releases so published assets and tags cannot be
  changed. The signing script attaches the signature while the release is still
  a draft.
- **Environment `release`:** add required reviewers so a build waits for a
  maintainer's approval.
- **Rulesets:** on `main`, require pull requests and passing CI and block force
  pushes and deletion. On `v*` tags, allow only maintainers to create tags, and
  block updates and deletion.
- **Actions:** set the default workflow permissions to read-only. Require
  approval before workflows run for outside contributors. Allow only GitHub's
  actions, `goreleaser/goreleaser-action`, and
  `namespacelabs/nscloud-cache-action`.

Actions are pinned to commit SHAs. Dependabot proposes updates to actions and Go
modules only after a seven-day cooldown. CI runs `go mod verify` and
`govulncheck`.

## Automatic updates

Builds from the Release workflow set `buildinfo.Release`. Once a day, the first
command they run starts `sliplane update --background` in a detached process and
continues without waiting. The update makes the same checks as `install.sh`,
then renames the new executable over the old one; the next command uses it and
prints `Updated Sliplane OLD → NEW`. The last check time lives in
`update.json` in the OS user cache directory.

Automatic updates are off when `CI` or `SLIPLANE_NO_AUTO_UPDATE` is set, and
for `go install`, `make build`, and other non-release builds. Users can always
run `sliplane update` to update immediately.

## Verify a release manually

`install.sh` runs the signature and checksum checks automatically:

```sh
gh release download v0.1.0 --repo sliplane/cli
curl -fsSL https://raw.githubusercontent.com/sliplane/cli/main/.github/allowed_signers > allowed_signers
ssh-keygen -Y verify -f allowed_signers -I sliplane-release -n sliplane-release \
  -s checksums.txt.sig < checksums.txt
shasum -a 256 -c checksums.txt --ignore-missing
gh attestation verify sliplane_0.1.0_linux_amd64.tar.gz --repo sliplane/cli  # public repository only
```

## Artifact contract

Each release includes:

- `sliplane_VERSION_darwin_amd64.tar.gz`
- `sliplane_VERSION_darwin_arm64.tar.gz`
- `sliplane_VERSION_linux_amd64.tar.gz`
- `sliplane_VERSION_linux_arm64.tar.gz`
- `checksums.txt`, which also covers `install.sh`
- `checksums.txt.sig`, the maintainer's YubiKey signature on `checksums.txt`
- `install.sh`

Archives contain `sliplane`, `LICENSE`, and `NOTICE`. `VERSION` excludes the tag's
leading `v`. The binary reports this version and the full Git commit hash.
