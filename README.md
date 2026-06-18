# TokiToki Agent

Cross-platform CLI that indexes local AI coding usage (Claude Code, Codex) and
uploads it to a TokiToki server.

The agent is a **stateless command-line tool, not a daemon**. Each invocation
runs one subcommand, writes a JSON result to stdout, and exits. There is no
long-lived process and no local HTTP server. Native front-ends (macOS, Windows,
Linux) are the same one shared agent driven by `exec` + stdout parsing; only the
UI is rewritten per platform.

Durable state lives on disk and is shared across invocations, so missing a run
loses nothing — the next scan catches up incrementally.

## Build

```sh
make build        # host binary into bin/tokitoki
make cross        # all platforms into dist/ (pure Go, no cgo)
```

## Data directory

The same path is used on macOS, Windows, and Linux, so every front-end resolves
it identically as `filepath.Join(os.UserHomeDir(), ".tokitoki")`:

```text
~/.tokitoki/
  api_key       shared server API key used for uploads
  config.json   non-secret agent settings
  usage.bolt    indexed local usage database
```

The directory name is configured in `internal/config/config.go`.

## Commands

Every command prints JSON to stdout; logs and errors go to stderr; exit code is
non-zero on failure.

```text
tokitoki scan                      Index changed Claude/Codex session files
tokitoki upload                    Upload indexed usage events to the server
tokitoki sync                      scan + upload (run this on a schedule)
tokitoki daily   [--provider all|claude|codex] [--project <name|path>]
                                   Summarize indexed usage by day/project
tokitoki claude-daily [--project <name|path>]
                                   Summarize Claude usage directly from files
tokitoki config get                Print settings
tokitoki config set [--api-key <k>] [--server-url <url>]
                                   Update settings
tokitoki status                    Print indexed event count, sources, config
tokitoki help                      Show help
```

`upload` posts indexed events to `<server_url>/api/usage-events/batch`. When
`server_url` is empty it defaults to `http://127.0.0.1:9093`.

## How front-ends use it

- **On demand (UI):** the tray/menu-bar app `exec`s e.g. `tokitoki daily
  --provider all` and renders the JSON.
- **Background cadence:** an OS scheduler runs `tokitoki sync` on an interval so
  the server stays current even when no UI is open — register at install time:
  - macOS: a `launchd` LaunchAgent
  - Linux: a `systemd --user` timer
  - Windows: a Task Scheduler task

## Example

```sh
tokitoki config set --api-key "$KEY" --server-url https://api.example.com
tokitoki sync
tokitoki daily --provider all --project tokitoki
```
