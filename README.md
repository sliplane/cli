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

Installed releases update themselves in the background once a day. Run
`sliplane update` to update now, or set `SLIPLANE_NO_AUTO_UPDATE=1` to opt out.

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
