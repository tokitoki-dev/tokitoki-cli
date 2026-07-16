# TokiToki TODO

## Known Issues (prioritized, reviewed 2026-07-17)

P0 — storage consolidation (done 2026-07-17):

- [x] Replace bbolt with SQLite for the agent's own state (`usage.db`). One
  table holds events and their upload queue state; bbolt is gone from go.mod.
  No migration: an existing `usage.bolt` is abandoned, provider events are
  rebuilt by the next scan and deduplicated server-side, unuploaded IDE
  heartbeats from before the switch are lost. The unused `source_files`
  tracking and the unqueried read APIs (`UsageEvents`, `CountEvents`,
  `DailyProjectSummaries`) were deleted with it.
- [x] Offline queue discipline: pending events upload oldest-first in batches
  of 1000 (max 5000 per run), the first failed request stops the run, failed
  events back off exponentially (30s doubling, capped at 1h), and uploaded
  events are pruned after 30 days.

P0 — dead scaffolding after the daemon→CLI refactor:

- [x] Remove the heartbeat chain. It POSTed to `<server>/heartbeats`, which the
  TokiToki server does not implement (only `/api/usage-events/batch` exists).
  Removed: `Heartbeat` type, `RecordHeartbeat`/`Sync`/`postHeartbeats`,
  `queue.jsonl` + store queue methods, and the `heartbeat`/`push-heartbeats`
  subcommands. (A new `heartbeat` subcommand exists today, but it feeds the
  regular `/api/usage-events/batch` pipeline through the shared queue — it is
  unrelated to the removed chain.)
- [x] Rewrite `status`: its in-memory fields were always empty in a CLI. Now
  reports indexed event count, source paths, `server_url`, and whether an
  api_key is set.

P1 — correctness/robustness:

- [x] Upload watermark: each event now has local upload state, so `tokitoki`
  uploads only pending/failed events and marks accepted/duplicate server
  responses as uploaded.
- [ ] Claude streaming token upsert (see WakaTime note below) — affects count
  accuracy.
- [ ] Replace hardcoded `InstallationID: "local-go-agent"` with a per-machine id
  persisted under `~/.tokitoki/`, so one user's multiple devices stay distinct.

P2 — missing basics:

- [x] `version` command / `--version`, with the version injected via ldflags.
- [ ] Scan lock + large-transcript protection (see WakaTime notes below).

P3 — cross-platform packaging:

- [ ] OS scheduler files running `tokitoki`: launchd `.plist`, systemd
  `--user` `.timer`/`.service`, Task Scheduler XML.
- [ ] `AGENT_PROTOCOL.md` documenting each subcommand's stdout JSON contract for
  the native front-ends.

## WakaTime CLI Ideas To Revisit

- Add a scan cursor similar to WakaTime's `ai_logs_last_parsed_at` so unchanged provider files are skipped. (The old `source_files` table was dead code and has been removed; a fresh design would add per-file rows to `usage.db`.)
- Add a short global scan lock so startup scan and manual `/usage/scan` cannot parse the same files concurrently.
- Add large transcript protection: bounded scanner buffers, max line size handling, and optional tail scanning for very large session files.
- Fix Claude streaming-update semantics: use a logical event key based on `provider + session_id + request_id + message_id`, then upsert latest token values instead of inserting a new event when token counts grow.
- Review Codex token handling against WakaTime's total-token/delta approach and decide whether TokiToki should store raw token-count events or normalized deltas.
- ~~Add upload queue behavior inspired by WakaTime offline sync~~ — done 2026-07-17: batch limits, stop-on-first-failure, exponential backoff, permanent rejection, and pruning all live in `usagedb` + `usageupload.SyncPending`.
- Add summary-level upload state so server sync sends daily/project/model summaries instead of every local `usage_event`.
- Add source status/debug endpoints for scan counts, last scan time, last error, indexed event count, and pending upload count.

## Repo Hygiene

- Keep local temp directories untracked.
- Keep generated SQLite databases and WAL/SHM files untracked.
