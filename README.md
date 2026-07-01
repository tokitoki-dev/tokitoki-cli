# TokiToki Agent

TokiToki is a small cross-platform uploader for local AI coding usage. Each
run reads the configured Claude Code and/or Codex session folders, then uploads
the discovered events to the local TokiToki server.

The CLI can run once or install itself as an OS service.

## Build

```sh
make build
```

`make` without a target builds the CLI and immediately runs it, which is the
fastest way to test a local upload against `localhost:9093`.

## Usage

```sh
# First run: save an API key.
tokitoki set key "$TOKITOKI_API_KEY"

# Later runs: scan and upload from ~/.claude and ~/.codex.
tokitoki

# Install/start as a user service where supported.
tokitoki service install
tokitoki service start
```

Options:

```text
--claude-dir        Claude data directory to scan; defaults to ~/.claude.
--codex-dir         Codex data directory to scan; defaults to ~/.codex.
```

Commands:

```text
set key <API_KEY>       Create or update ~/.tokitoki/api_key.
get key                 Print the API key from ~/.tokitoki/api_key.
service install         Install tokitoki as a service.
service uninstall       Remove the installed service.
service start           Start the installed service.
service stop            Stop the installed service.
service restart         Restart the installed service.
service status          Print service status.
```

Normal runs and `service install` default to `~/.claude` and `~/.codex`; pass
`--claude-dir`, `--codex-dir`, or `--interval` after the service subcommand to override.
The service integration uses `github.com/kardianos/service`, so Linux systemd,
OpenRC, SysV, Upstart, macOS launchd, and Windows services use the same CLI
surface. Service installs default to a user service; pass `--system` after the
service action to request a system service:

```sh
tokitoki service install --system
```

The upload target is fixed at:

```text
http://localhost:9093/api/usage-events/batch
```

The API key is stored under `~/.tokitoki/`. The local database in that directory
prevents unchanged source files from being parsed again on later runs.
