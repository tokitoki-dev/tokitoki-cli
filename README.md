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
# First run: save an API key and choose the local clients to read.
echo "$TOKITOKI_API_KEY" | tokitoki --api-key-stdin --providers claude,codex

# Later runs: scan and upload using the saved configuration.
tokitoki
```

Options:

```text
--api-key-stdin     Read and persist the API key from standard input.
--providers         Comma-separated local clients: claude,codex.
```

The upload target is fixed at:

```text
http://localhost:9093/api/usage-events/batch
```

The API key and selected providers are stored under `~/.tokitoki/`. The local
database in that directory prevents unchanged source files from being parsed
again on later runs.
