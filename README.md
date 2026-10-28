# Sliplane CLI

![Status: alpha](https://img.shields.io/badge/status-alpha-orange)

Deploy and manage your [Sliplane](https://sliplane.io) services, servers,
PostgreSQL databases, and S3 buckets from the terminal.

```sh
sliplane deploy n8n --name workflows   # deploy a preset
sliplane logs workflows --follow       # stream its logs
sliplane ssh workflows                 # open a shell inside it
sliplane postgres connect main         # psql-style shell, nothing to install
```

> The CLI is in alpha. Commands and JSON output may still change between
> releases.

## Install

macOS, Linux, and WSL (x86_64 and ARM64):

```sh
curl -fsSL https://sliplane.io/install.sh | sh
```

This installs to `~/.sliplane/bin` and offers to add it to your PATH. Run it
again to update. The installer only accepts releases signed by a Sliplane
maintainer (requires OpenSSH 8.2+).

<details>
<summary>Other ways to install</summary>

**A specific version:** replace `0.1.0` in both places.

```sh
curl -fsSL https://github.com/sliplane/cli/releases/download/v0.1.0/install.sh | sh -s -- 0.1.0
```

**With Go:**

```sh
go install github.com/sliplane/cli/cmd/sliplane@latest
```

**Manually:** download an archive from the
[releases page](https://github.com/sliplane/cli/releases) and put the
`sliplane` binary on your PATH.

</details>

## Get started

Sign in to [sliplane.io](https://sliplane.io) in your browser and select your
team. Then:

```sh
sliplane auth login       # approve the login in your browser
sliplane service list     # list services in all projects
```

Resources can be referenced by name or ID. Save a default project so you
don't need `--project` every time:

```sh
sliplane config set project my-project
```

Run `sliplane --help`, or `--help` on any command, for every option.

## What you can do

| Command | |
| --- | --- |
| `service` | Create from an image or GitHub repo, update, redeploy, pause, delete. Manage env vars, domains, and volumes. |
| `deploy` | Create a service from a dashboard preset (`sliplane preset list`). |
| `logs`, `service metrics`, `service events` | Read or follow logs, chart CPU and memory (`--watch`), see deploy history. |
| `ssh`, `tunnel` | Open a shell in a service, or forward a local port to it. Uses your installed OpenSSH. |
| `postgres` | Create, resize, restore, and connect to databases. Run queries and inspect slow queries, sizes, and connections. |
| `s3` | Manage buckets, access keys, and CORS. Upload and download files. |
| `server`, `project` | Create, scale, and delete servers. Manage server volumes and projects. |
| `registry`, `ssh-key` | Manage private registry credentials and persistent SSH keys. |

A few examples:

```sh
sliplane service create web --image docker.io/library/nginx:1.28 --public --server main
sliplane service env set web API_KEY 'value'
sliplane service domain add web example.com
sliplane tunnel api --local-port 8080
sliplane postgres query main 'SELECT now();'
sliplane s3 upload uploads ./report.pdf
```

## Authentication

`sliplane auth login` opens your browser and stores the token in your OS
keychain. You stay logged in as long as you use the CLI at least once a week.
Check who you are with `sliplane auth status`.

**Multiple teams:** each team gets its own profile. Logging in to a new team
asks whether to overwrite the current profile or create a new one. Switch with
`sliplane profile use NAME` or `--profile NAME`.

**CI and scripts:** create an API token in the Sliplane dashboard and pass it
in the environment. Commands that change resources need a read-write token.

```sh
export SLIPLANE_TOKEN=...
sliplane service redeploy api --project production
```

## Scripting

- Add `-o json` to any command for machine-readable output (JSON Lines with
  `logs --follow`). Make it the default with `sliplane config set output json`.
- Commands never prompt with JSON output or outside a terminal. Commands that
  would ask for confirmation require `--yes` instead.
- Shell completion: `sliplane completion bash|zsh|fish|powershell --help`.

| Environment variable | |
| --- | --- |
| `SLIPLANE_TOKEN` | API token. Overrides the saved login. |
| `SLIPLANE_PROFILE` | Profile to use. |
| `SLIPLANE_S3_ACCESS_KEY_ID`, `SLIPLANE_S3_SECRET_ACCESS_KEY` | Bucket credentials for `s3` file commands. |

## Coding agents

`sliplane agent init` adds a short Sliplane guide to `AGENTS.md` (or
`CLAUDE.md` in Claude Code) so coding agents know how to use the CLI safely.

## Development

Requires the Go version in [go.mod](go.mod).

```sh
make build && ./bin/sliplane --help
make check    # vet and tests
```

Maintainers: see [docs/releasing.md](docs/releasing.md) for how releases are
built and signed. To report a vulnerability, see [SECURITY.md](SECURITY.md).

## License

[Apache 2.0](LICENSE)
