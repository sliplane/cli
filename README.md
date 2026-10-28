# Sliplane CLI

![Status: alpha](https://img.shields.io/badge/status-alpha-orange)

Deploy and manage [Sliplane](https://sliplane.io) services, servers,
PostgreSQL databases, and S3 buckets from your terminal.

## Install

macOS, Linux, and WSL:

```sh
curl -fsSL https://sliplane.io/install.sh | sh
```

Or with Go: `go install github.com/sliplane/cli/cmd/sliplane@latest`.
Binaries are on the [releases page](https://github.com/sliplane/cli/releases).

## Install with your coding agent

Paste this into Claude Code, Codex, Cursor, or any other coding agent:

```text
Install the Sliplane CLI on my machine, not in a sandbox or container: run `curl -fsSL https://sliplane.io/install.sh | sh`, add `~/.sliplane/bin` to PATH in my shell profile if it isn't there yet, and confirm it works with `sliplane version`. If you can't run commands directly on my machine, don't install it anywhere else. Instead, give me step-by-step instructions for my OS and shell: what to paste into my terminal, how to add it to PATH, and how to check it worked.
```

## Get started

```sh
sliplane auth login                   # log in through your browser
sliplane service list                 # list your services
sliplane deploy n8n --name workflows  # deploy a preset
sliplane logs workflows --follow      # stream its logs
sliplane ssh workflows                # open a shell in it
```

Run `sliplane --help` to see everything: services, servers, PostgreSQL, S3,
domains, volumes, metrics, tunnels, and more.

## Authentication

Browser logins are saved in your OS keychain and renew automatically while
you use the CLI at least once a week. For CI, set `SLIPLANE_TOKEN` to an API
token from the dashboard. Use `--profile` to work with more than one team.

## Scripting

Add `-o json` to any command for structured output. Commands never prompt
outside a terminal; pass `--yes` to confirm deletions.

## Development

```sh
make build && ./bin/sliplane --help
make check
```

See [docs/releasing.md](docs/releasing.md) for releases and
[SECURITY.md](SECURITY.md) to report a vulnerability. Licensed under
[Apache 2.0](LICENSE).
