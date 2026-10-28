# Sliplane CLI

![Status: alpha](https://img.shields.io/badge/status-alpha-orange)

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
terminals. Run the same command again to update. The installer only installs
releases signed by a Sliplane maintainer's hardware key (requires OpenSSH 8.2+).

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

Logging in to a different team prompts you to overwrite the current profile,
create a new profile, or cancel. Overwriting clears the previous team's saved
project and S3 credentials. Creating a profile keeps the old one and selects the
new one. For scripts, use `sliplane auth login --profile NAME` with a new name.

## Project settings

Save a default project for your current profile:

```sh
sliplane config set project my-project
```

Override it for a single command with `--project`:

```sh
sliplane service list --project another-project
```

## Connect to a service

```sh
sliplane tunnel my-service
```

Uses the service's internal port locally if available, otherwise a free port,
and prints the local address to connect to.
If the service port is unknown, it tries HTTP on port 80.
Press Ctrl-C to close the tunnel. To choose ports yourself:

```sh
sliplane tunnel my-service --local-port 8080 --remote-port 3000
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
