# Sliplane CLI

CLI for sliplane.io

## Installation

macOS and Linux (including WSL), on Intel/AMD x86_64 or ARM64. Run in bash or zsh:

```sh
curl -fsSL https://github.com/sliplane/cli/releases/latest/download/install.sh | sh -s -- --setup-path && \
  export PATH="$HOME/.sliplane/bin:$PATH"
```

The installer verifies the download and checks that the executable runs, then
installs to `~/.sliplane/bin` without sudo. The command automatically configures PATH for
this terminal and future bash/zsh sessions, without prompting. Then log in:

```sh
sliplane auth login
```

Run the installer again to update or repair your installation. An identical,
executable installation is left unchanged. To install a specific release, use
its version in both the URL and argument (replace `0.1.0` with a published version):

```sh
curl -fsSL https://github.com/sliplane/cli/releases/download/v0.1.0/install.sh | sh -s -- 0.1.0 --setup-path && \
  export PATH="$HOME/.sliplane/bin:$PATH"
```

Set `SLIPLANE_INSTALL_DIR` on the `sh` command to choose another directory,
and use that directory in the `export PATH` line too.
For CI or installation without shell profile changes, replace `--setup-path`
with `--non-interactive`. Other shells receive manual PATH instructions.
[Release archives](https://github.com/sliplane/cli/releases) are also available
for manual installation. These commands require a published release.

Maintainers: see [the release process](docs/releasing.md).
