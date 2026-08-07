# tokenwarden

Budget-aware scheduler for Claude Code work. See [`docs/REQUIREMENTS.md`](docs/REQUIREMENTS.md) for the full spec and [`docs/SPIKE-001-usage-telemetry.md`](docs/SPIKE-001-usage-telemetry.md) for the research that shaped the architecture.

## Module layout

- `module tokenwarden` — no real VCS path yet, renamed trivially with `go mod edit -module` once this has a GitHub home.
- `cmd/tokenwardend` — the daemon. Owns the store, the scheduler, the HTTP API.
- `cmd/tokenwarden` — CLI client. Talks to the daemon over HTTP; never touches the store directly.
- `cmd/twprobe` — the statusline shim (REQUIREMENTS.md §6.1 item 1, passive capture path): installed as Claude Code's `statusLine` command via `tokenwarden probe install`, it reads stdin on every render, POSTs any `rate_limits` it sees to `POST /api/ground-truth` under a tight timeout, and prints a minimal passthrough line. Must never block a Claude Code turn (NFR-PERF-3) — a slow or unreachable daemon just means nothing gets reported that render.
- `internal/config` — config file + env loading, shared by both binaries.
- `internal/store` — SQLite persistence (`modernc.org/sqlite`, no cgo — this is load-bearing, see below). Lowest layer; no business logic.
- `internal/queue` — job lifecycle rules (dependency gating, priority) on top of `store`.
- `internal/api` — HTTP handlers on top of `queue`, `dispatch`, and `budget`. Thin.
- `internal/runner` — spawns `claude -p`, parses `stream-json` into a structured `Result`. Depends only on `store` (for `store.Job`), never on `queue`.
- `internal/budget` — three of REQUIREMENTS.md §6.1's sensor-fusion sources: the local ledger (`RecordResult` persists a dispatch's exact tokens/cost per model from `runner.Result`; `FiveHourTotal`/`SevenDayTotal` sum a rolling window — tokenwarden's own exact spend, **not** the plan's actual window fill), ground truth (`RecordGroundTruth`/`LatestGroundTruth` store what `cmd/twprobe` captures — the only authoritative source of window fill tokenwarden has, when a reading exists), and calibration (`CalibrateFiveHour`/`CalibrateSevenDay` fit tokens-per-percent from ground-truth reading deltas paired with ledger totals, exposed read-only via `GET /api/usage` and `tokenwarden usage` — not yet wired into any dispatch or throttling decision, since the scheduler doesn't exist yet). Depends only on `store` and `runner` (for the `Result` type).
- `internal/dispatch` — runs one named job now (`DispatchOne`/`RunJob`, on top of `runner`, `queue`, and `budget`) and appends its usage to the ledger on completion. Not a scheduler: nothing here selects a job or repeats on its own — that's a future phase's job.
- `internal/backfill` — FR-USAGE-2: indexes `~/.claude/projects/**/*.jsonl` (the transcripts Claude Code itself writes for every interactive session) into the ledger via `budget.Ledger.RecordHistoricalUsage`, so usage totals aren't blind to work done outside tokenwarden. Idempotent by transcript-line `uuid` (`store.RecordUsageIfNew`'s `source_uuid` uniqueness), so re-indexing on every daemon start is safe and cheap. No cost data exists in the transcript format, so backfilled entries always carry `CostUSD: 0` — a known limitation, not a bug. Depends only on `budget`.
- `internal/cliclient` — HTTP client shared by `cmd/tokenwarden`.

Dependencies point inward: `api → dispatch → {runner, queue, budget} → store`; `budget → runner` (for the `Result` type only); `backfill → budget`; `cmd/twprobe → internal/api` (for wire DTOs only, no store/business-logic dependency). Nothing in `store` imports `queue`, `runner`, `budget`, `dispatch`, `backfill`, or `api`; `runner` never imports `queue` or `budget`.

## Hard constraints

- **No cgo, anywhere.** `modernc.org/sqlite` only. This is what lets `GOOS=windows GOARCH=amd64 go build` succeed from this Mac with no C toolchain, which is what makes cross-platform CD cheap. A dependency that pulls in cgo transitively is a blocker, not a tradeoff to weigh.
- **No credentials.** tokenwarden never reads, stores, or transmits an API key, OAuth token, or password. It shells out to the user's already-authenticated `claude` CLI. See `docs/REQUIREMENTS.md` §4.2.
- **Never `--dangerously-skip-permissions`.** Not as a default, not as a flag, not behind a config option.

## Commands

```bash
task build   # build all binaries into ./bin
task test    # go test ./... -race
task lint    # go vet + golangci-lint
task run     # build + run the daemon in foreground with local config
```

(`Taskfile.yml` at repo root; install via `go install github.com/go-task/task/v3/cmd/task@latest` or see https://taskfile.dev.)

## Testing

Integration tests for the runner and dispatch layers use a **fake `claude` binary** under `internal/runner/testdata/fakeclaude` that emits scripted `stream-json`, so the dispatch path is exercised with zero tokens spent and no network. Never write a test that shells out to the real `claude` CLI — it costs money and requires a live login. `cmd/twprobe`'s integration tests build the real `twprobe` binary and run it against an `httptest.Server` standing in for the daemon — no Claude Code session needed, since twprobe only cares about the JSON shape on stdin, which SPIKE-001 already captured and verified.

## Current phase

Phase 3 complete (`internal/runner` + `internal/dispatch`: a job can now actually be run, via `POST /api/jobs/{id}/dispatch` or `tokenwarden queue dispatch <id>`, always as an explicit single-job trigger — there is still no automatic/background dispatch loop). A completed job's final text output is persisted (`store.Job.Result`, threaded through `queue.Finish`/`dispatch.RunJob`) and surfaced via `GET /api/jobs/{id}`'s `result` field and `tokenwarden queue show`.

Phase 4 is in progress, one slice at a time (per REQUIREMENTS.md §6.1's three sensor-fusion sources):

- **Done:** the local ledger (`internal/budget`) — exact tokens/cost per dispatch, rolling 5h/7d totals, exposed via `GET /api/usage` and `tokenwarden usage`.
- **Done:** ground truth, passive capture path only (`cmd/twprobe` + `internal/budget`'s `RecordGroundTruth`/`LatestGroundTruth` + `POST /api/ground-truth`) — install via `tokenwarden probe install`, which merges the shim into `~/.claude/settings.json`'s `statusLine` command. The user's own interactive sessions now feed the daemon real `rate_limits` readings for free, surfaced in `GET /api/usage`'s `ground_truth` field.
- **Done:** FR-USAGE-2 historical backfill (`internal/backfill`) — on daemon startup, a background goroutine indexes `~/.claude/projects/**/*.jsonl` and feeds every interactive session's usage into the same ledger `tokenwarden usage` reads from, idempotently (safe to re-run every startup). Interactive-session entries are tagged `job_id = "interactive:<sessionID>"` and carry `CostUSD: 0` (not present in the transcript format).
- **Done:** calibration fitting (`internal/budget/calibration.go`) — learns tokens-per-percent from ground-truth reading deltas paired with ledger totals, exposed via `GET /api/usage` and `tokenwarden usage`. Read-only: not yet consulted by any dispatch decision.
- **Not started:** the active capture path (a periodic PTY sentinel — needs a live session to validate and a cadence/cost tradeoff decision, REQUIREMENTS.md §10 open question 1) and the dispatch loop / `internal/scheduler` (aggressiveness, reserved blocks, weekly pacing). Several of §10's open questions (sentinel cadence, cold-start conservatism) block finishing these until there's real usage data to measure against, not just code.

See `docs/REQUIREMENTS.md` §"Implementation phases" via the plan history, or just check what packages exist under `internal/`.
