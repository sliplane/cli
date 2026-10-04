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

Current release blocker: the existing macOS race suite reports a race in
`github.com/muesli/cancelreader` during `TestDeployInteractiveDestination` in
`internal/cli/deploy_prompt_test.go`. Resolve this before publishing; CI retains
the race check and does not suppress the failure.

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
Keep these names stable so the installer and a future updater can share them.

The installer resolves `latest` once, then downloads the archive and checksum
from that exact tag. SHA-256 verifies download integrity; it is not an independent
signature proving publisher identity. Downloads use HTTPS. Artifact signing and
macOS Developer ID signing/notarization are not configured in this first pipeline.

Downloads are verified before installation. A temporary executable is staged in
the destination directory, and its `version -o text` command must run successfully
and report the requested version before it is renamed into place. The previous
binary remains intact if downloading or verification fails. The installer refuses
to overwrite a symlink, so it cannot follow one into a package-manager installation.

The documented install command uses `--setup-path` to configure bash/zsh without
prompting. When run without that flag in a terminal, PATH setup follows
[Fly's installer](https://fly.io/install.sh): it offers to configure PATH,
defaulting to yes. It reads the answer from `/dev/tty`, so the prompt works when
the script is piped into `sh`. It appends
to `.zshrc` (respecting `ZDOTDIR`) or `.bashrc`, falling back to an existing
`.bash_profile` when `.bashrc` is absent. Missing `.zshrc`/`.bashrc` files are
created during setup. Repeated installs do not duplicate the generated line.
Unlike Fly, we resolve release assets directly from GitHub and verify checksums.

Use `--non-interactive` to suppress prompts, and `--setup-path` to explicitly
request shell setup even in noninteractive mode. Redirected output skips setup
unless `--setup-path` is given. Unsupported shells or profile write failures get
manual instructions; the installed binary remains usable. Automatic setup omits
redundant PATH instructions on success. For manual setup, the installer reports
any different `sliplane` selected by the current PATH. The documented install
command appends `&& export PATH="$HOME/.sliplane/bin:$PATH"`, which runs in the
user's current shell and makes the CLI available immediately. Shell profile
setup persists PATH for future terminals. When running the script on its own,
users can run the printed PATH command to update their current shell.

## Set up the short installation URL

Configure `https://sliplane.io/install` on the website to redirect to:

```text
https://github.com/sliplane/cli/releases/latest/download/install.sh
```

Use a temporary redirect (302 or 307), so it can be changed later. This website
route is outside this repository and is not created by the release workflow.
Once configured and tested, the README's default command can become:

```sh
curl -fsSL https://sliplane.io/install | sh -s -- --setup-path && export PATH="$HOME/.sliplane/bin:$PATH"
```

## Updates and recovery

For now users rerun the installer. It reports whether it installed, updated,
reinstalled, or found an identical executable already installed. Only a first
installation prints the login hint. A repeat install still downloads and verifies
the release, but leaves an identical executable in place; different contents or
missing execute permissions are repaired even when the version is unchanged.

The version argument also allows installing a previous published version;
it does not create a persistent version pin. If an
installation becomes unusable, rerun the installer to repair it. To remove the
default installation, remove `~/.sliplane/bin/sliplane` and the PATH entry added
to your shell profile; credentials and configuration remain separate.

If a published release is broken, stop marking it as latest and select the last
working stable release while preparing a new version. Do not replace published
archives or move their tags. Leave automatic update checks, channels, background
processes, and persistent pinning for the later updater work.
