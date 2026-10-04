# Sliplane CLI

A command-line interface for [Sliplane](https://sliplane.io). Deploy and manage
services, servers, S3 buckets, and PostgreSQL databases from your terminal.

## Installation

### Install script

For macOS, Linux, and WSL on x86_64 or ARM64. Run in bash or zsh:

```sh
curl -fsSL https://github.com/sliplane/cli/releases/latest/download/install.sh | sh -s -- --setup-path && \
  export PATH="$HOME/.sliplane/bin:$PATH"
```

Installs to `~/.sliplane/bin` and sets up PATH for your current and future
terminals. Run the same command again to update.

To install a specific version, replace `0.1.0` in both places with a published
release:

```sh
curl -fsSL https://github.com/sliplane/cli/releases/download/v0.1.0/install.sh | sh -s -- 0.1.0 --setup-path && \
  export PATH="$HOME/.sliplane/bin:$PATH"
```

### Download from GitHub

Download the archive for your platform from the
[Releases page](https://github.com/sliplane/cli/releases), extract it, and place
the `sliplane` binary in a directory on your PATH.

## Getting started

1. Sign in to [Sliplane](https://sliplane.io) in your browser, select your team,
   then log in from a terminal on the same computer:

   ```sh
   sliplane auth login
   ```

2. List your services:

   ```sh
   sliplane service list
   ```

3. Inspect a service by name or ID:

   ```sh
   sliplane service show my-service
   ```

Use `sliplane --help` to explore commands, or append `--help` to any command
for its options.

Browser login grants full access to the browser's active team. The access token
is stored in your OS keychain and expires after 24 hours. Run `sliplane auth login`
again to renew it; automatic refresh is not included yet. If you cancel in the
browser, press Ctrl+C in the terminal or wait for the five-minute timeout.

For CI or a remote terminal, use `SLIPLANE_TOKEN` or `--token` with an API token.
An explicit token takes precedence over browser login. You can save one with
`sliplane auth login --token TOKEN`. Use `--profile NAME` for a separate team
profile. Existing profiles stay bound to their team.

`sliplane auth logout` removes the selected profile's local token; it does not
revoke the token on the server.

## Project settings

Save a default project for your current profile:

```sh
sliplane config set project my-project
```

Override it for a single command with `--project`:

```sh
sliplane service list --project another-project
```

## Building from source

With Go installed (see [go.mod](go.mod) for the required version):

```sh
make build
./bin/sliplane --help
```

Run checks with `make check`.

## Releases

Tagged releases are built with GoReleaser and published through GitHub Releases.
See [the release process](docs/releasing.md) for maintainer instructions.
