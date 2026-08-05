# tokenwarden

Budget-aware scheduler for Claude Code work. See [`docs/REQUIREMENTS.md`](docs/REQUIREMENTS.md) for the full spec and [`docs/SPIKE-001-usage-telemetry.md`](docs/SPIKE-001-usage-telemetry.md) for the research that shaped the architecture.

## Module layout

- `module tokenwarden` — no real VCS path yet, renamed trivially with `go mod edit -module` once this has a GitHub home.
- `cmd/tokenwardend` — the daemon. Owns the store, the scheduler, the HTTP API.
- `cmd/tokenwarden` — CLI client. Talks to the daemon over HTTP; never touches the store directly.
- `internal/config` — config file + env loading, shared by both binaries.
- `internal/store` — SQLite persistence (`modernc.org/sqlite`, no cgo — this is load-bearing, see below). Lowest layer; no business logic.
- `internal/queue` — job lifecycle rules (dependency gating, priority) on top of `store`.
- `internal/api` — HTTP handlers on top of `queue` and `dispatch`. Thin.
- `internal/runner` — spawns `claude -p`, parses `stream-json` into a structured `Result`. Depends only on `store` (for `store.Job`), never on `queue`.
- `internal/budget` — the local usage ledger: `RecordResult` persists a dispatch's exact tokens/cost (per model, from `runner.Result`), `FiveHourTotal`/`SevenDayTotal` sum a rolling window. This is tokenwarden's own exact spend, **not** the plan's actual rate-limit window fill — that needs ground truth (a future `twprobe` statusline shim / PTY sentinel, still to come) plus calibration. Depends only on `store` and `runner` (for the `Result` type).
- `internal/dispatch` — runs one named job now (`DispatchOne`/`RunJob`, on top of `runner`, `queue`, and `budget`) and appends its usage to the ledger on completion. Not a scheduler: nothing here selects a job or repeats on its own — that's a future phase's job.
- `internal/cliclient` — HTTP client shared by `cmd/tokenwarden`.

Dependencies point inward: `api → dispatch → {runner, queue, budget} → store`; `budget → runner` (for the `Result` type only). Nothing in `store` imports `queue`, `runner`, `budget`, `dispatch`, or `api`; `runner` never imports `queue` or `budget`.

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

Integration tests for the runner and dispatch layers use a **fake `claude` binary** under `internal/runner/testdata/fakeclaude` that emits scripted `stream-json`, so the dispatch path is exercised with zero tokens spent and no network. Never write a test that shells out to the real `claude` CLI — it costs money and requires a live login.

## Current phase

Phase 3 complete (`internal/runner` + `internal/dispatch`: a job can now actually be run, via `POST /api/jobs/{id}/dispatch` or `tokenwarden queue dispatch <id>`, always as an explicit single-job trigger — there is still no automatic/background dispatch loop).

Phase 4 is in progress, one slice at a time (per REQUIREMENTS.md §6.1's three sensor-fusion sources):

- **Done:** the local ledger (`internal/budget`) — exact tokens/cost per dispatch, rolling 5h/7d totals, exposed via `GET /api/usage` and `tokenwarden usage`.
- **Not started:** ground truth (`twprobe` statusline shim + PTY sentinel — needs a new small binary and a live session to validate), calibration (learns tokens-per-percent from ground-truth deltas), and the dispatch loop / `internal/scheduler` (aggressiveness, reserved blocks, weekly pacing). Several of REQUIREMENTS.md §10's open questions (sentinel cadence, cold-start conservatism) block finishing these until there's real usage data to measure against, not just code.

See `docs/REQUIREMENTS.md` §"Implementation phases" via the plan history, or just check what packages exist under `internal/`.
