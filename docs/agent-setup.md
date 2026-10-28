# Set up your coding agent for the Sliplane CLI

Configure the agent you are running in to use the Sliplane CLI. Do the setup yourself where your tools allow it. Ask the user only for steps that need them, such as signing in through the browser.

## 1. Install the CLI

Run `sliplane version`. If it succeeds, the CLI is installed; keep the existing installation. Otherwise, install it on macOS, Linux, or WSL with:

```sh
curl -fsSL https://sliplane.io/install.sh | sh
```

The script installs to `~/.sliplane/bin` (override with an absolute `SLIPLANE_INSTALL_DIR`). Without a terminal it does not edit shell profiles, so add the directory to `PATH` yourself: append `export PATH="$HOME/.sliplane/bin:$PATH"` to the user's shell profile (`~/.zshrc`, `~/.bashrc`, or the equivalent for their shell) unless it is already there. Use the full path `~/.sliplane/bin/sliplane` for the rest of this session, and tell the user to open a new terminal.

On native Windows, use WSL or download the binary from https://github.com/sliplane/cli/releases. If Go is available and the user prefers it, `go install github.com/sliplane/cli/cmd/sliplane@latest` works too. Run `sliplane version` again to confirm.

## 2. Authenticate

Run `sliplane auth status`. If it shows a team and token access, keep the existing login.

Otherwise, ask the user to run `sliplane auth login` in their own terminal and finish signing in through the browser. The login is saved in their OS keychain. If they use more than one team, ask which profile to use and pass `--profile NAME` to later commands.

In CI or other non-interactive environments, the user supplies an API token from the Sliplane dashboard through the `SLIPLANE_TOKEN` environment variable. Never ask the user to paste a token into chat, print its value, put it in shell history, or commit it.

Run `sliplane auth status` again to confirm. It reports whether the token can only read or can also make changes.

## 3. Add Sliplane guidance to the project

From the project root, run:

```sh
sliplane agent init --yes
```

This writes a marked Sliplane section to `AGENTS.md`, or to `CLAUDE.md` when running in Claude Code. Instructions outside that section are preserved, and running it again updates the section. Read the section afterwards; it describes how to work with Sliplane safely through the CLI.

## 4. Verify and report

Run a read-only command such as `sliplane project list -o json`. Do not create, deploy, restart, or delete resources to test the setup. Setup does not authorize infrastructure changes.

Report the installed version, the team and access level from `sliplane auth status`, which guidance file was written, and any step the user still has to finish. Never claim setup is complete if authentication has not succeeded.
