# TokiToki TODO

## Known Issues (prioritized, reviewed 2026-06-18)

P0 — dead scaffolding after the daemon→CLI refactor:

- [x] Remove the heartbeat chain. It POSTed to `<server>/heartbeats`, which the
  TokiToki server does not implement (only `/api/usage-events/batch` exists).
  Removed: `Heartbeat` type, `RecordHeartbeat`/`Sync`/`postHeartbeats`,
  `queue.jsonl` + store queue methods, and the `heartbeat`/`push-heartbeats`
  subcommands.
- [x] Rewrite `status`: its in-memory fields were always empty in a CLI. Now
  reports indexed event count, source paths, `server_url`, and whether an
  api_key is set.

P1 — correctness/robustness:

- [ ] Upload watermark: every `tokitoki` invocation resends the entire history
  (server dedups). Track an "already uploaded" cursor locally and send only the
  delta. (Related: "summary-level upload state" below.)
- [ ] Claude streaming token upsert (see WakaTime note below) — affects count
  accuracy.
- [ ] Open the DB read-only for `daily`/`claude-daily` so reads don't block on a
  concurrent `sync` writer (currently waits up to the 5s bolt lock timeout then
  errors).
- [ ] Replace hardcoded `InstallationID: "local-go-agent"` with a per-machine id
  persisted under `~/.tokitoki/`, so one user's multiple devices stay distinct.

P2 — missing basics:

- [ ] `version` command / `--version`, with the version injected via ldflags.
- [ ] Scan lock + large-transcript protection (see WakaTime notes below).

P3 — cross-platform packaging:

- [ ] OS scheduler files running `tokitoki`: launchd `.plist`, systemd
  `--user` `.timer`/`.service`, Task Scheduler XML.
- [ ] `AGENT_PROTOCOL.md` documenting each subcommand's stdout JSON contract for
  the native front-ends.

## WakaTime CLI Ideas To Revisit

- Add a scan cursor similar to WakaTime's `ai_logs_last_parsed_at`, but keep `source_files` as the durable per-file state for TokiToki.
- Add a short global scan lock so startup scan and manual `/usage/scan` cannot parse the same files concurrently.
- Add large transcript protection: bounded scanner buffers, max line size handling, and optional tail scanning for very large session files.
- Fix Claude streaming-update semantics: use a logical event key based on `provider + session_id + request_id + message_id`, then upsert latest token values instead of inserting a new event when token counts grow.
- Review Codex token handling against WakaTime's total-token/delta approach and decide whether TokiToki should store raw token-count events or normalized deltas.
- Add upload queue behavior inspired by WakaTime offline sync: batch limits, retry, requeue on transient failures, discard/mark rejected records on permanent 400 responses.
- Add summary-level upload state so server sync sends daily/project/model summaries instead of every local `usage_event`.
- Add source status/debug endpoints for scan counts, last scan time, last error, indexed event count, and pending upload count.

## Repo Hygiene

- Keep local temp directories untracked.
- Keep generated SQLite databases and WAL/SHM files untracked.
