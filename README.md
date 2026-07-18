# tokitoki-cli

TokiToki is a small cross-platform uploader for local AI coding usage. Each
run reads the configured local agent usage folders, then uploads the discovered
events to the local TokiToki server.

The CLI can run once or install itself as an OS service.

## Build

```sh
make build
```

`make` without a target builds the CLI and immediately runs it. Set
`TOKITOKI_BASE_URL=http://localhost:9093` to test against a local server.

## Usage

```sh
# First run: save an API key.
tokitoki set key "$TOKITOKI_API_KEY"

# Later runs: scan and upload from the default provider directories.
tokitoki

# Install/start as a user service where supported.
tokitoki service install
tokitoki service start
```

Options:

```text
--provider-dir      Provider data directory to scan as provider=dir; repeatable.
                    Defaults to the built-in provider directories below.
```

Environment:

```text
TOKITOKI_BASE_URL     Server base URL; defaults to https://tokitoki.dev.
```

Commands:

```text
set key <API_KEY>       Create or update ~/.tokitoki/api_key.
get key                 Print the API key from ~/.tokitoki/api_key.
get dashboard-url       Print a one-time URL that opens the web dashboard signed in.
version                 Print the CLI version ("dev" for local builds).
heartbeat               Record one IDE activity event and flush the upload queue.
upgrade                 Replace this binary with the newest published release.
service install         Install tokitoki as a service.
service uninstall       Remove the installed service.
service start           Start the installed service.
service stop            Stop the installed service.
service restart         Restart the installed service.
service status          Print service status.
```

## Stable project names

TokiToki normally uses the project name reported by an IDE or local AI agent.
To give a checkout a stable name across editors, machines, and differently
named local folders, create `.tokitoki-project` in the project root:

```text
customer-portal
release/2026
```

The first line overrides the project name. The optional second line overrides
the branch. An empty file uses the containing folder's name and preserves any
branch reported by the editor. The nearest project file found by walking up
from the active file wins; for out-of-tree agent files, TokiToki also searches
the event's reported project path.

Use `{project}` in the first line to include the nearest Git, Mercurial, or
Subversion root folder dynamically:

```text
my-company/{project}
```

For a file inside a `payments-api` repository this resolves to
`my-company/payments-api`. Without version control, `{project}` becomes the
folder containing `.tokitoki-project`.

Existing `.wakatime-project` files use the same two-line format and are
accepted as a compatibility fallback. When both files exist in one folder,
`.tokitoki-project` takes precedence. A `.wakatime` file is different: it is a
WakaTime project-level INI configuration file and is not treated as a project
name by TokiToki.

Project-file resolution is applied centrally before events enter the local
queue, so it affects IDE heartbeats and AI-agent usage scans consistently. It
does not rename events that were already uploaded.

Normal runs and `service install` default to these provider roots:

```text
claude=~/.claude
codex=~/.codex
copilot=~/.copilot/otel
gemini=~/.gemini/tmp
kimi=~/.kimi
qwen=~/.qwen
openclaw=~/.openclaw
openclaw=~/.clawdbot
openclaw=~/.moltbot
openclaw=~/.moldbot
pi=~/.pi/agent/sessions
amp=~/.local/share/amp
droid=~/.factory/sessions
kilo=~/.local/share/kilo
hermes=~/.hermes
codebuff=~/.config/manicode
codebuff=~/.config/manicode-dev
codebuff=~/.config/manicode-staging
opencode=~/.local/share/opencode
goose=~/.local/share/goose/sessions/sessions.db
goose=~/Library/Application Support/goose/sessions/sessions.db
goose=~/.local/share/Block/goose/sessions/sessions.db
```

Pass one or more `--provider-dir provider=dir` values or `--interval` after the
service subcommand to override. The service integration uses
`github.com/kardianos/service`, so Linux systemd, OpenRC, SysV, Upstart, macOS
launchd, and Windows services use the same CLI surface. Service installs default
to a user service; pass `--system` after the service action to request a system
service:

```sh
tokitoki service install --system
```

## The shared CLI

Every TokiToki front-end — the macOS and Windows apps and every editor
plugin — invokes one shared copy of this CLI. Its location is a contract, not
a suggestion; a plugin that resolves a different path forks the fleet and
stops receiving updates:

```text
~/.tokitoki/bin/tokitoki            macOS, Linux
%USERPROFILE%\.tokitoki\bin\tokitoki.exe   Windows
```

The `bin/` segment keeps executables apart from the data files that live in
`~/.tokitoki` itself (`api_key`, the local database, lock files).

Rules every front-end and plugin must follow:

1. **Resolve the shared binary first**; fall back to your bundled copy only
   when the shared one is missing or not executable.
2. **Seed, never download.** When the shared binary is missing, or reports an
   older version than your bundled copy, copy the bundled binary into the
   shared path — staged next to the destination and renamed into place, so
   the swap is atomic. Never overwrite a newer shared binary, and never
   fetch the CLI from the network yourself.
3. **Delegate freshness to the CLI.** Invoke `tokitoki upgrade` (silent, safe
   to fire and forget) at launch and on a slow timer. The CLI owns the whole
   check–download–verify–swap sequence.

The reference implementation of all three rules is `AgentProcess.swift` in
the macOS app.

## Self-update

`tokitoki upgrade` checks the server's `cli` release channel, downloads the
new binary, verifies it reports the offered version, and renames it over the
running executable — atomically, so concurrent invocations see either the old
binary or the new one. The service worker runs the same check every 12 hours
and exits after a successful update so the service manager relaunches the new
binary. Local builds (version `dev`) never self-update.

The default upload target is:

```text
https://tokitoki.dev/api/usage-events/batch
```

Override the server for local development or staging:

```sh
TOKITOKI_BASE_URL=http://localhost:9093 tokitoki
```

## Local data and the offline queue

The API key and a SQLite database (`usage.db`) live under `~/.tokitoki/`. The
database is a write-ahead upload queue: every discovered event is stored as
`pending` first, then uploaded in batches. When the network is down the run
fails fast and the events simply stay queued; each failed attempt backs off
exponentially (30s doubling up to 1h), so an offline machine retries calmly
instead of hammering the server on every heartbeat. The next heartbeat or
sync after connectivity returns drains the queue automatically — there is no
separate recovery mechanism to configure. Uploaded events are pruned after 30
days; events the server rejects permanently are kept for inspection but never
retried.

## License

Licensed under the [Apache License, Version 2.0](LICENSE).
