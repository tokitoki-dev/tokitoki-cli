# TokiToki Agent

TokiToki is a small cross-platform uploader for local AI coding usage. Each
run reads the configured Claude Code and/or Codex session folders, then uploads
the discovered events to the local TokiToki server.

There are no report, status, scan-only, or upload-only commands. Running the
CLI always performs the complete operation.

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

# Later runs: scan and upload using the saved configuration.
tokitoki --claude-dir ~/.claude --codex-dir ~/.codex
```

Options:

```text
--claude-dir        Claude data directory to scan; omit to skip Claude.
--codex-dir         Codex data directory to scan; omit to skip Codex.
```

Commands:

```text
set key <API_KEY>   Create or update ~/.tokitoki/api_key.
get key             Print the API key from ~/.tokitoki/api_key.
```

The upload target is fixed at:

```text
http://localhost:9093/api/usage-events/batch
```

The API key is stored under `~/.tokitoki/`. The local database in that directory
prevents unchanged source files from being parsed again on later runs.
