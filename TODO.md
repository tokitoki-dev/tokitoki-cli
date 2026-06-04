# TrackLM TODO

## WakaTime CLI Ideas To Revisit

- Add a scan cursor similar to WakaTime's `ai_logs_last_parsed_at`, but keep `source_files` as the durable per-file state for TrackLM.
- Add a short global scan lock so startup scan and manual `/usage/scan` cannot parse the same files concurrently.
- Add large transcript protection: bounded scanner buffers, max line size handling, and optional tail scanning for very large session files.
- Fix Claude streaming-update semantics: use a logical event key based on `provider + session_id + request_id + message_id`, then upsert latest token values instead of inserting a new event when token counts grow.
- Review Codex token handling against WakaTime's total-token/delta approach and decide whether TrackLM should store raw token-count events or normalized deltas.
- Add upload queue behavior inspired by WakaTime offline sync: batch limits, retry, requeue on transient failures, discard/mark rejected records on permanent 400 responses.
- Add summary-level upload state so server sync sends daily/project/model summaries instead of every local `usage_event`.
- Add source status/debug endpoints for scan counts, last scan time, last error, indexed event count, and pending upload count.

## Repo Hygiene

- Keep local temp directories untracked.
- Keep generated SQLite databases and WAL/SHM files untracked.
