# tokitoki-cli

The engine behind [Tokitoki](https://tokitoki.dev): one static binary that
scans the local data of your AI coding agents, Claude Code, Codex, GitHub
Copilot, Gemini CLI and [a dozen more](#supported-tools), and uploads their
token usage and cost to your dashboard, grouped by project and shown next to
your coding time.

Runs once per invocation, or continuously as an OS service. No daemon, no
runtime, no config file to write.

## Install

Download a binary from the
[releases page](https://github.com/tokitoki-dev/tokitoki-cli/releases) for
macOS, Linux, or Windows (amd64 / arm64), or build from source:

```sh
make build
./bin/tokitoki version
```

Every release ships a `checksums.txt`; verify with `sha256sum --check`.

The Tokitoki desktop apps and editor plugins bundle this CLI, so you only
need to install it yourself for headless machines or scripting.

## Usage

```sh
# Save your API key (from https://tokitoki.dev/settings).
tokitoki set key <API_KEY>

# Scan and upload once.
tokitoki

# Or keep it running in the background.
tokitoki service install
tokitoki service start
```

```text
sync [OPTIONS]                Scan and upload usage (default command)
service <action>              install | uninstall | start | stop | restart | status
set key <API_KEY>             Store API key
get key | dashboard-url       Retrieve stored settings
verify key [<KEY>]            Test an API key
stats [--days N]              Local usage stats as JSON
upload enable | disable       Turn uploading on or off
update                        Install the latest version
version                       Show version
```

Run `tokitoki help <command>` for details.

Options:

```text
--provider-dir PROVIDER=DIR   Scan a specific provider directory; repeatable
--interval DURATION           Sync interval in service mode
--check-update                Self-update after the sync
```

Set `TOKITOKI_NO_TELEMETRY=1` to disable the anonymous install ping.

On Linux the service is a systemd timer. Run `service install` with `sudo`
on servers to install system units that survive reboots; without it, a user
unit is installed. macOS and Windows use launchd and Windows services.

## Supported tools

Claude Code, Codex, GitHub Copilot, Gemini CLI, Kimi, Qwen, OpenClaw, Pi,
Amp, Droid, Kilo, Hermes, Codebuff, OpenCode, Goose, WorkBuddy. Default directories are
scanned automatically; use `--provider-dir` to add or override one.

## Project names

By default the project name comes from the IDE or agent. To pin a stable name
across machines and editors, add a `.tokitoki` file to the project root:

```text
customer-portal
release/2026
```

Line one is the project name, line two (optional) the branch. `{project}`
expands to the nearest Git, Mercurial, or Subversion root folder name, e.g.
`my-company/{project}`.

## Other clients

[VS Code](https://github.com/tokitoki-dev/tokitoki-vscode) ·
[macOS](https://github.com/tokitoki-dev/tokitoki-macos) ·
[Windows](https://github.com/tokitoki-dev/tokitoki-windows). All of them
bundle this CLI. The overview lives at
[github.com/tokitoki-dev](https://github.com/tokitoki-dev).

## Development

Work on `dev`; releases are tagged from `main`. See
[RELEASING.md](RELEASING.md).

Local builds report to `http://localhost:9093` and keep state in
`~/.tokitoki-dev`; check with `tokitoki server-url` and `tokitoki data-dir`.

## License

[Apache License 2.0](LICENSE)
