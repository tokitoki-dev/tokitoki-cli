# TrackLM Go Agent

MVP daemon for TrackLM. It exposes a local HTTP API on `127.0.0.1:39391`, stores heartbeats locally, and can batch-sync them to a configured server.

## Run

```sh
go run ./cmd/tracklm-agent
```

The agent stores local data under the OS config directory:

```text
~/Library/Application Support/TrackLM/
```

## API

`GET /health` is public. All other endpoints require:

```text
Authorization: Bearer <token>
```

The token is generated at:

```text
~/Library/Application Support/TrackLM/agent.token
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

The agent scans Claude and Codex session files into a local SQLite database at:

```text
~/Library/Application Support/TrackLM/usage.bolt
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
  -H "Authorization: Bearer $(cat "$HOME/Library/Application Support/TrackLM/agent.token")" \
  -H "Content-Type: application/json" \
  -d '{"entity":"/Users/me/project/main.go","project":"tracklm","language":"Go","editor":"VSCode","type":"file"}'
```
