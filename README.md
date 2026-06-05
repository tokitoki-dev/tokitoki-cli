# TokiToki Go Agent

MVP daemon for TokiToki. It exposes a local HTTP API on `127.0.0.1:39391`, stores heartbeats locally, and can batch-sync them to a configured server.

## Run

```sh
go run ./cmd/tracklm-agent
```

On startup, the agent creates a local data directory. By default it uses:

```text
~/.goagent/
```

This directory name is configured in Go code at `internal/config/config.go`.
Change `config.DataDirName` and rebuild the agent if the default needs to be
different.

At startup the agent also copies existing files from the old
`~/Library/Application Support/TrackLM/` directory when the new directory does
not already contain them.

Important files in the data directory:

```text
api_key       shared server API key used for uploads
agent.token   local loopback API token
config.json   non-secret agent settings
queue.jsonl   queued heartbeat data
usage.bolt    indexed local usage database
```

## API

`GET /health` is public. All other endpoints require:

```text
Authorization: Bearer <token>
```

The token is generated at:

```text
~/.goagent/agent.token
```

Endpoints:

```text
GET  /status
GET  /settings
PUT  /settings
POST /heartbeat
POST /sync
GET  /usage/daily
POST /usage/scan
GET  /claude/usage/daily
POST /quit
```

The agent scans Claude and Codex session files into a local BoltDB database at:

```text
~/.goagent/usage.bolt
```

`POST /usage/scan` scans changed local session files. `GET /usage/daily` summarizes indexed AI token usage. Query parameters:

```text
provider=all|claude|codex
project=<project name or path>
```

`POST /usage/upload` uploads indexed usage events to:

```text
<server_url>/api/usage-events/batch
```

When `server_url` is empty, the agent defaults usage uploads to:

```text
http://127.0.0.1:9093
```

Example heartbeat:

```sh
curl -X POST http://127.0.0.1:39391/heartbeat \
  -H "Authorization: Bearer $(cat "$HOME/.goagent/agent.token")" \
  -H "Content-Type: application/json" \
  -d '{"entity":"/Users/me/project/main.go","project":"tokitoki","language":"Go","editor":"VSCode","type":"file"}'
```
