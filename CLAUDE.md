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
- `internal/dispatch` — runs one named job now (`DispatchOne`/`RunJob`, on top of `runner`, `queue`, and `budget`) and appends its usage to the ledger on completion. Not a scheduler itself: nothing here selects which job to run — that's `internal/scheduler`, on top of this. Also owns the global kill switch (FR-SAFE-4, `Halt`/`Resume`/`Halted`, in-memory only): halting fails every future dispatch fast and cancels every in-flight run's context, which terminates its subprocess.
- `internal/backfill` — FR-USAGE-2: indexes `~/.claude/projects/**/*.jsonl` (the transcripts Claude Code itself writes for every interactive session) into the ledger via `budget.Ledger.RecordHistoricalUsage`, so usage totals aren't blind to work done outside tokenwarden. Idempotent by transcript-line `uuid` (`store.RecordUsageIfNew`'s `source_uuid` uniqueness), so re-indexing on every daemon start is safe and cheap. No cost data exists in the transcript format, so backfilled entries always carry `CostUSD: 0` — a known limitation, not a bug. Depends only on `budget`.
- `internal/scheduler` — the budget-aware dispatch loop (REQUIREMENTS.md §6.2): `Engine.Tick` refreshes fused usage state (ground truth if fresh, else ledger converted through `budget` calibration — `Source` is `SourceUnknown` rather than a guess when neither exists), applies the admission checks (`Decide`: reserved blocks, the aggressiveness ceiling on the 5h/7d windows with §6.3's safety margin), and dispatches the next runnable job via `dispatch.DispatchOne` when nothing holds it back — one job at a time, synchronously, per §10 item 2/3. `Engine.Run` ticks on a 30s cadence (`TickInterval`) until its context is cancelled; a `Clock` seam (`RealClock`/`SimClock`) lets tests drive a simulated week instantly. Depends on `store` (for `SchedulerConfig`), `queue`, `dispatch`, and `budget`. Not yet implemented: §6.2 steps 5-6 (burn-rate throttling/concurrency — nothing here runs more than one job at a time yet) and §6.4's cost-predicted oversized-job fitting (`queue.DeferOversized` exists for a future slice to call).
- `internal/cliclient` — HTTP client shared by `cmd/tokenwarden`.
- `ui/` — the local web UI (NFR-UI-2: "a local web app served by the daemon; the desktop window is a thin shell over it" — the desktop shell itself doesn't exist yet, this is web-only). React 18 + TypeScript + Vite, no router/state library (a ~15-line hash router in `src/router.ts`, plain `useState`/`useEffect` against the typed fetch wrapper in `src/api/client.ts`). `src/api/types.ts` mirrors every `internal/api` DTO field-for-field — keep it in sync by hand when the Go DTOs change, nothing generates it. Dev: `task ui:dev` (Vite on :5173, proxies `/api` to the daemon per `TOKENWARDEN_API_PROXY_TARGET`, default `127.0.0.1:7842` — no CORS handling needed anywhere since the browser only ever talks to one origin). Production: `task ui:build` outputs `ui/dist`; `internal/api/static.go`'s `Server.MountUI` serves it with SPA fallback if the configured `TOKENWARDEN_UI_DIST_DIR` (default `ui/dist`, CWD-relative) exists, and is a silent no-op (API-only mode) if it doesn't — deliberately filesystem-based rather than `go:embed`, so `go build ./...` and Go-only CI never depend on a UI build existing. Pages: Dashboard (usage + kill switch), Jobs list, Create Job, Job detail, Scheduler config — maps 1:1 onto the existing API surface, no new backend endpoints. Node version pinned via `ui/.nvmrc` (24; the system default was 16, too old for current Vite).

Dependencies point inward: `api → dispatch → {runner, queue, budget} → store`; `scheduler → {store, queue, dispatch, budget}`; `budget → runner` (for the `Result` type only); `backfill → budget`; `cmd/twprobe → internal/api` (for wire DTOs only, no store/business-logic dependency); `ui/ → internal/api` (wire shape only, via hand-mirrored TS types — no code-level dependency, just a contract both sides need to keep in sync). Nothing in `store` imports `queue`, `runner`, `budget`, `dispatch`, `scheduler`, `backfill`, or `api`; `runner` never imports `queue` or `budget`.

## Hard constraints

- **No cgo, anywhere.** `modernc.org/sqlite` only. This is what lets `GOOS=windows GOARCH=amd64 go build` succeed from this Mac with no C toolchain, which is what makes cross-platform CD cheap. A dependency that pulls in cgo transitively is a blocker, not a tradeoff to weigh.
- **No credentials.** tokenwarden never reads, stores, or transmits an API key, OAuth token, or password. It shells out to the user's already-authenticated `claude` CLI. See `docs/REQUIREMENTS.md` §4.2.
- **Never `--dangerously-skip-permissions`.** Not as a default, not as a flag, not behind a config option.

## Commands

```bash
task build     # build all binaries into ./bin — Go-only, works with no Node installed
task test      # go test ./... -race
task lint      # go vet + golangci-lint
task run       # build + run the daemon in foreground with local config
task build:all # task build + task ui:build — everything, requires Node
task ui:install # npm install in ui/
task ui:dev     # Vite dev server on :5173, proxies /api to a running daemon
task ui:build   # production UI build to ui/dist, served by the daemon if present
task ui:lint    # oxlint (typecheck + lint) for ui/
```

(`Taskfile.yml` at repo root; install via `go install github.com/go-task/task/v3/cmd/task@latest` or see https://taskfile.dev. `task build`/`test`/`lint` deliberately stay Go-only — no Node dependency was added to the existing CI path.)

## Testing

Integration tests for the runner and dispatch layers use a **fake `claude` binary** under `internal/runner/testdata/fakeclaude` that emits scripted `stream-json`, so the dispatch path is exercised with zero tokens spent and no network. Never write a test that shells out to the real `claude` CLI — it costs money and requires a live login. `cmd/twprobe`'s integration tests build the real `twprobe` binary and run it against an `httptest.Server` standing in for the daemon — no Claude Code session needed, since twprobe only cares about the JSON shape on stdin, which SPIKE-001 already captured and verified.

## Current phase

Phase 3 complete (`internal/runner` + `internal/dispatch`: a job can now actually be run, via `POST /api/jobs/{id}/dispatch` or `tokenwarden queue dispatch <id>`, always as an explicit single-job trigger — there is still no automatic/background dispatch loop). A completed job's final text output is persisted (`store.Job.Result`, threaded through `queue.Finish`/`dispatch.RunJob`) and surfaced via `GET /api/jobs/{id}`'s `result` field and `tokenwarden queue show`.

FR-SAFE-4's global kill switch is also done: `POST /api/kill-switch/halt`, `POST /api/kill-switch/resume`, `GET /api/kill-switch`, and `tokenwarden kill-switch halt|resume|status` — halting cancels every in-flight dispatch's subprocess and fails future dispatches immediately, in-memory only (a daemon restart clears it).

**Phase 4 is complete** (per REQUIREMENTS.md §6.1's three sensor-fusion sources), plus one extra slice bundled in alongside it:

- **Done:** the local ledger (`internal/budget`) — exact tokens/cost per dispatch, rolling 5h/7d totals, exposed via `GET /api/usage` and `tokenwarden usage`.
- **Done:** ground truth, passive capture path only (`cmd/twprobe` + `internal/budget`'s `RecordGroundTruth`/`LatestGroundTruth` + `POST /api/ground-truth`) — install via `tokenwarden probe install`, which merges the shim into `~/.claude/settings.json`'s `statusLine` command. The user's own interactive sessions now feed the daemon real `rate_limits` readings for free, surfaced in `GET /api/usage`'s `ground_truth` field.
- **Done:** calibration fitting (`internal/budget/calibration.go`) — learns tokens-per-percent from ground-truth reading deltas paired with ledger totals, exposed via `GET /api/usage` and `tokenwarden usage`. Read-only: not yet consulted by any dispatch decision.
- **Done (bonus, not one of the three sensor-fusion sources but shipped in the same phase):** FR-USAGE-2 historical backfill (`internal/backfill`) — on daemon startup, a background goroutine indexes `~/.claude/projects/**/*.jsonl` and feeds every interactive session's usage into the same ledger `tokenwarden usage` reads from, idempotently (safe to re-run every startup). Interactive-session entries are tagged `job_id = "interactive:<sessionID>"` and carry `CostUSD: 0` (not present in the transcript format).

**Phase 5 slice 1 is complete: the dispatch loop.** `internal/scheduler` (see above) implements REQUIREMENTS.md §6.2's tick loop — reserved-block and aggressiveness-ceiling admission checks, fused usage state, and dispatching the queue's highest-priority runnable job. It's wired into `cmd/tokenwardend`: the daemon always runs `scheduler.Engine.Run` in the background, ticking every 30s, but every tick is a no-op until a user opts in via `tokenwarden scheduler config set --enabled` (or `PUT /api/scheduler/config`) — see `GET`/`PUT /api/scheduler/config` and `tokenwarden scheduler config show|set`. Covered by simulated-clock integration tests against the fake `claude` binary (empty queue, oversubscribed queue/priority ordering, a reserved block, disabled scheduler, reaching/clearing the 5h ceiling).

Not yet done, left for a later Phase 5 slice:
- **§6.2 steps 5-6 (burn-rate throttling / concurrency).** Nothing dispatches more than one job at a time yet — REQUIREMENTS.md §10 item 2 (concurrency) is still open and unmeasured, so there's no burn rate to widen or narrow against.
- **§6.4 oversized-job fitting.** `queue.DeferOversized` exists as a primitive, but nothing calls it yet: there's no per-job cost predictor to decide a job is oversized in the first place, and REQUIREMENTS.md §10 item 3's resolution (no prior, one job at a time, rely on the hard ceiling) is exactly what this slice already does without needing prediction.
- **The active capture path (a periodic PTY sentinel).** Needs a real interactive `claude` session inside a pseudo-terminal to force a statusline render (headless `-p` mode never emits `rate_limits` — SPIKE-001's finding), and nothing in this repo has prototyped PTY handling yet (Unix PTYs vs. Windows ConPTY is unexplored). Treat it as its own live-validation spike — similar to SPIKE-001 — before writing production code.
- **Preferred windows (FR-SCHED-3)** are stored (`SchedulerConfig.PreferredWindows`) but not yet consulted by `Decide` — only reserved blocks currently gate dispatch. What "actively try to use" should mean operationally (widen the ceiling during a preferred window vs. gate dispatch to only preferred windows, e.g.) isn't resolved in REQUIREMENTS.md §10 — needs a policy call before implementing, not just wiring.
- ~~Reserved blocks / preferred windows aren't yet settable from the CLI~~ **Done:** `tokenwarden scheduler config set --reserved-block`/`--preferred-window` (repeatable, format `[Days:]HH:MM-HH:MM`, e.g. `Mon,Tue,Wed,Thu,Fri:09:00-17:00` or `22:00-06:00` for a wraparound block with no day restriction) and `--clear-reserved-blocks`/`--clear-preferred-windows`. `tokenwarden scheduler config show` now prints each block instead of just a count.

See `docs/REQUIREMENTS.md` §"Implementation phases" via the plan history, or just check what packages exist under `internal/`.

PR #13 merged; the CI failures on it were an Actions billing/runner-provisioning issue (private repo), not a code problem. Resolved by making the repo public (`garrett928/tokenwarden`) — public repos get unlimited free Actions minutes, so `.github/workflows/ci.yml`'s full OS matrix (`test` × 3 OSes, `cross-compile` × 5 targets on `macos-latest`) runs as originally designed; no workflow trimming needed.

**Basic local web UI (`ui/`, see Module layout above) is done** — a first UI milestone, not one of the numbered phases in `docs/REQUIREMENTS.md`'s original plan. Dashboard, Jobs list, Create Job, Job detail, and Scheduler config pages, all verified against a real running daemon in a browser (dev mode via `task ui:dev` and production mode via `task ui:build` + the daemon's static serving). One real bug found and fixed during verification: `GET /api/scheduler/config` used to serialize `reserved_blocks`/`preferred_windows` as JSON `null` instead of `[]` when empty (Go nil-slice marshaling), which crashed the UI's `TimeBlockEditor` on first load — fixed in `internal/api/scheduler.go`'s `newSchedulerConfigResponse`, regression-tested in `internal/api/scheduler_test.go`'s `TestGetSchedulerConfig_EmptyBlockListsAreArraysNotNull`. Next: a native desktop shell (Wails, per NFR-UI-2) is a separate, later phase; the "not yet done" Phase 5 items above remain open.
