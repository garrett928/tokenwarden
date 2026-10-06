# tokenwarden — Requirements

**Status:** Draft v1, revision 2 · **Last updated:** 2026-10-05

**Revision 2 (2026-10-05)** adds §5.6 (workspace, project context, attachments, output folder), amends FR-JOB-2/3/5, FR-SAFE-2/3, NFR-UI-2 and §10, and adds FR-SAFE-6/7. It is a deliberate, user-approved change to the safety posture — see the note under FR-SAFE-3. Implementation guidance for the next agent: [`WORKSPACE-AND-ATTACHMENTS-PLAN.md`](WORKSPACE-AND-ATTACHMENTS-PLAN.md).

---

## 1. Problem

A Claude Pro/Max subscription is governed by two rolling windows: a **5-hour session window** and a **7-day weekly window**, shared across claude.ai, Claude Desktop, and Claude Code. Unused capacity in either window **does not roll over** — it is gone.

The result is systematic waste. The 5-hour window silently refills while you sleep, commute, or sit in meetings. Weekly capacity expires unused not because there is no work to do, but because nobody was at the keyboard to dispatch it.

**tokenwarden** closes that gap. It is a cross-platform desktop application that holds a queue of real work and dispatches it against your own Claude subscription, paced so that weekly capacity lands as close to fully consumed as you choose — while reserving headroom during the hours you want Claude for yourself.

### 1.1 Guiding principle

tokenwarden schedules **real queued work**. It does not manufacture busywork to burn quota. The queue is the primary object in the system; "keep the agent busy" is deliberately not a feature. The goal is to stop wasting capacity you have already paid for, not to consume capacity for its own sake.

---

## 2. Goals and non-goals

### 2.1 Goals

- Consume a user-chosen fraction (up to ~100%) of weekly subscription capacity on work the user actually wants done.
- Make token consumption legible: when it happened, on what model, at what cost, driven by what.
- Turn that legibility into concrete, evidence-backed optimization advice.
- Run unattended overnight, safely, without leaving a workspace in a broken state.
- Stay small and quiet enough to leave running permanently.

### 2.2 Non-goals

- **Not** a Claude API client. tokenwarden never holds an API key or a credential (see §4.2).
- **Not** a reseller or proxy. It automates one user's own CLI on their own machine for their own work.
- **Not** a replacement for interactive Claude Code. It complements it and accounts for it.
- **Not** a general-purpose CI system. Scheduling is budget-driven, not event-driven.

---

## 3. Verified research findings

Everything in this section was **verified against Anthropic's official documentation and by direct experiment** during planning on 2026-08-01, against Claude Code v2.1.152. It is load-bearing: the requirements below depend on it.

### 3.1 Usage telemetry — what exists

| Channel | Gives us | Verdict |
|---|---|---|
| **`statusLine` command JSON on stdin** | `rate_limits.five_hour.{used_percentage, resets_at}`, `rate_limits.seven_day.{…}` | **The only machine-readable feed for plan-limit state.** Free — consumes no tokens. Pro/Max subscribers only, and only after the first API response in a session. |
| **`-p --output-format json/stream-json`** | Exact per-run `usage` (input/output/cache-read/cache-creation, split by 1h vs 5m TTL), `modelUsage` per model, `total_cost_usd`, `session_id` | Excellent ledger data. Contains **no** rate-limit information. |
| **`~/.claude/projects/**/*.jsonl`** | Full historical per-message `usage` + model + timestamp, already on disk | Months of free retroactive history for analytics. |
| **OpenTelemetry** (`CLAUDE_CODE_ENABLE_TELEMETRY=1`) | `claude_code.token.usage`, `claude_code.cost.usage` attributed by `agent.name`, `skill.name`, `plugin.name`, `mcp_server.name`, `mcp_tool.name`, `effort` | Best attribution available. Confirmed to contain **no** rate-limit/quota metric. |
| **Hooks** | Session/tool lifecycle | Fire in `-p` mode, but payload carries **no** usage or rate-limit fields. |
| **`/usage`** | Everything, prettily | TUI screen only, not scriptable. |
| **Admin / Analytics APIs** | Per-user cost reports | Team/Enterprise + API key only. Not applicable. |

### 3.2 Spike results (SPIKE-001)

Three experiments, run against a live Pro/Max account. See `docs/SPIKE-001-usage-telemetry.md` for method and raw output.

1. **`statusLine` does NOT fire under `claude -p`.** Injecting a `statusLine` via `--settings` in headless mode produced zero probe invocations. Headless runs cannot self-report usage.
2. **`statusLine` DOES fire in a PTY-attached interactive session,** and `rate_limits` is populated on the render following the first API response — not on the initial render. A PTY sentinel is therefore a *validated* mechanism, not a speculative one.
3. **`used_percentage` is an integer, not a float.** Observed values `89` and `22`; the documentation example shows `23.5`. **Resolution is 1%**, which bounds achievable calibration precision and must be absorbed by the pacing math (§6.3).

### 3.3 Consequences for the architecture

- Jobs execute via `-p` (clean, scriptable, exact ledger data) — but are **blind to plan limits** while running.
- Plan-limit ground truth must come from a separate PTY-attached channel.
- Between ground-truth fixes, window state is **dead-reckoned** from the local ledger plus a learned calibration. This is estimation, and the product must present it as such.

---

## 4. Execution model

### 4.1 Backend

**FR-EXEC-1** — tokenwarden executes all work by spawning the user's locally installed, already-authenticated `claude` binary as a subprocess.

**Rationale:** the Claude Agent SDK expects API-key authentication, which bills per token against the Console and **does not draw down the subscription pool**. Anthropic's Agent SDK documentation further states that third-party developers may not offer claude.ai login or rate limits for their products. Since consuming subscription capacity is the entire purpose of tokenwarden, driving the user's own CLI is both the only mechanism that works and the only one that is appropriate.

**FR-EXEC-2** — tokenwarden never stores, transmits, reads, or prompts for an API key, OAuth token, or password. Authentication is entirely the CLI's concern.

**FR-EXEC-3** — the runner must tolerate CLI version drift: check `claude --version` at startup, treat all optional JSON fields as absent-able, and degrade to reduced functionality rather than crashing when a field disappears.

### 4.2 CLI primitives used

Verified against `claude --help` (v2.1.152):

| Flag | Role |
|---|---|
| `-p --output-format stream-json --verbose` | Job execution and live event streaming |
| `--model`, `--effort <low\|medium\|high\|xhigh\|max>` | Per-job model and thinking effort (**FR-JOB-4**) |
| `--max-budget-usd` | Hard per-job spend ceiling; also the mechanism for partial runs (§6.4) |
| `--permission-mode <plan\|dontAsk\|acceptEdits\|…>` | Per-job-kind autonomy profile |
| `--resume <id>`, `--session-id <uuid>` | Continuation across windows, follow-up turns |
| `--worktree`, `--add-dir` | Filesystem isolation for unattended runs; extra readable/writable folders |
| process working directory (`cmd.Dir`) | Where Claude Code loads project context from: `CLAUDE.md`, `.claude/skills`, commands, subagents, settings (**FR-WS-2**) |
| `--json-schema` | Structured results for research jobs |
| `--settings <json>` | Per-run statusline injection (interactive sessions only) |
| `--agents`, `--mcp-config`, `--plugin-dir` | Future integration surface |
| `system/api_retry` event with `error: "rate_limit"` | Authoritative backoff trigger |

---

## 5. Functional requirements

### 5.1 Queue and jobs

- **FR-JOB-1** — Users can queue prompts and tasks for later execution, with priority ordering.
- **FR-JOB-2** — A job carries: kind, prompt, workspace directory (optional, any kind — §5.6), extra directories, output directory (optional, §5.6), model, effort, attachments, priority, optional earliest-start and deadline, optional per-job budget cap, dependencies, and optional user-declared steps.
- **FR-JOB-3** — Job kinds map to CLI flag profiles: `research`, `plan`, `code`, `review`, `freeform`. Kinds are presets for permission mode and defaults, not capability ceilings (FR-SAFE-3, revision 2). Every kind can use the workspace's project skills (FR-WS-2).
- **FR-JOB-4** — Users choose model and thinking effort per job, with a configurable default.
- **FR-JOB-5** — Users can attach files and screenshots, including pasted images. Superseded in detail by FR-ATT-1..8 (§5.6): attachments are snapshotted into daemon-owned storage and referenced by absolute path in the prompt; Claude Code's `Read` tool handles images and PDFs natively.
- **FR-JOB-6** — Jobs may depend on other jobs; a job is not dispatched until its dependencies succeed.
- **FR-JOB-7** — Jobs support multi-turn continuation via `--resume`, including user-authored follow-ups on a completed job.
- **FR-JOB-8** — Job states: `queued`, `blocked`, `running`, `paused_budget`, `deferred_oversized`, `succeeded`, `failed`, `cancelled`.

### 5.2 Scheduling and pacing

- **FR-SCHED-1** — A **token aggressiveness** setting (0–100%) controls how much of weekly capacity the scheduler targets and how full it allows the 5-hour window to get.
- **FR-SCHED-2** — Users define **reserved blocks**: recurring time ranges during which the scheduler either dispatches nothing or holds to a reduced ceiling, preserving capacity for personal interactive use.
- **FR-SCHED-3** — Users define **preferred windows**: times the scheduler should actively try to use (e.g. overnight).
- **FR-SCHED-4** — The scheduler paces toward the weekly target across the whole week rather than draining the budget early. Weekly capacity is the scarce resource; a fully drained window on Monday is an outage on Friday.
- **FR-SCHED-5** — On an authoritative rate-limit signal (`api_retry` with `error: "rate_limit"`, or a session/weekly limit error), the scheduler halts dispatch until the reported reset time. Prediction is an optimization; the error is the brake.
- **FR-SCHED-6** — The scheduler runs unattended, including overnight, without a UI window open.
- **FR-SCHED-7** — When the highest-priority job does not fit remaining headroom, the scheduler attempts to run *part* of it before falling through to a smaller job (§6.4).

### 5.3 Usage visibility (the Overview section)

- **FR-USAGE-1** — Display current 5-hour and 7-day window fill with reset countdowns, clearly labelled as measured vs. estimated.
- **FR-USAGE-2** — On first run, index `~/.claude/projects/**/*.jsonl` to backfill historical usage, so the Overview is populated with months of real data on day one — including the user's own interactive sessions, not only tokenwarden jobs.
- **FR-USAGE-3** — **Timeline view**: usage by hour/day/week, stacked by model, overlaid with 5-hour window boundaries and reserved blocks.
- **FR-USAGE-4** — **Breakdowns** by model, effort level, job kind, project/workspace, skill, subagent, MCP server, and interactive-vs-scheduled — sortable by tokens, cost, and share of weekly cap.
- **FR-USAGE-5** — **Token composition** over time: fresh input vs. cache-read vs. cache-write vs. output. Cache behaviour is where most invisible spend lives.
- **FR-USAGE-6** — **Waste report**: unused capacity per completed 5-hour window, and weekly capacity left unused at each weekly reset, trended over time. This is the metric that justifies the product.
- **FR-USAGE-7** — Optional one-click enablement of Claude Code's OpenTelemetry export pointed at a local OTLP receiver, to add per-skill / per-subagent / per-MCP-server attribution that transcripts do not carry.
- **FR-USAGE-8** — **Optimization insights**, each with supporting evidence and an estimated saving, linking to the jobs or sessions that produced it:
  - Cache misses (cache-write dominating cache-read after idle gaps)
  - Long-running sessions with climbing per-turn cost
  - Model fit (what the same workload would have cost on a cheaper model)
  - Effort level overuse
  - MCP server / skill / subagent overhead
  - Agent teams (~7× a standard session)
  - Idle capacity ("you could have run N more research jobs this week")

  No insight fires without concrete numbers behind it. Insights are dismissible.

- **FR-USAGE-9** — Display prediction error (estimated vs. measured window fill) as a first-class quality metric, so the user can calibrate their trust in the estimates.

### 5.4 Results and artifacts

- **FR-ART-1** — Each job runs in a watched workspace; created and modified files are recorded, diffed, and browsable in the UI, with images and Markdown rendered inline.
- **FR-ART-2** — Live streaming of a running job's output, including tool calls and subagent activity.
- **FR-ART-3** — Full job history with prompt, flags used, token cost, duration, and outcome.

### 5.5 Integrations (post-v1)

- **FR-INT-1** — A `WorkSource` plugin interface (`Poll() []CandidateWork`) allows external systems to supply queueable work. **Stubbed in v1; no provider ships in v1.**
- **FR-INT-2** — Planned providers, in priority order: GitHub Issues/PRs/Projects, then ClickUp, then Google Drive.

---

### 5.6 Workspace, project context, attachments and output (added revision 2)

Goal: queuing a job should feel like starting a Claude session in a folder — the folder's own `CLAUDE.md` and skills apply, files can be attached the way they can in the Claude desktop app, and generated files can be sent somewhere chosen. Everything in this section applies to **every** job kind.

**Workspace (the folder Claude runs from)**

- **FR-WS-1** — `workspace` is optional on every kind (today `code` requires it). When set, it is the process working directory of the `claude` subprocess.
- **FR-WS-2** — Project context applies. Because Claude Code is started in the workspace, that folder's `CLAUDE.md` (and parents'), `.claude/skills`, commands, subagents and settings are loaded, along with the user's own user-level equivalents. This must hold for `research`, `plan`, `review`, `code` and `freeform` alike, which requires the `Skill` tool to be allowed in every kind (FR-SAFE-3).
- **FR-WS-3** — With no workspace, the job runs in a per-job scratch directory (`<data dir>/jobs/<job-id>/work`), never in the daemon's own working directory. (A packaged desktop app's daemon has `/` as its cwd, so "inherit the daemon's cwd" is not an acceptable default.) Attachments always live beside it (`.../attachments`).
- **FR-WS-4** — The daemon validates a workspace (exists, is a directory, readable) and can describe it: git repo or not, `CLAUDE.md` present, number of project skills, project hooks present, `.mcp.json` present. The UI shows this as chips before queuing (FR-UI-2). An invalid path is rejected at job creation.
- **FR-WS-5** — The UI's workspace field has a folder-picker icon that opens the **native OS folder dialog** (Finder on macOS) in the desktop shell and fills in the absolute path. Typing or pasting a path always works; in a plain browser (no shell) the typed path is the only option.
- **FR-WS-6** — A job's workspace is immutable once it has a session id (resume is expected to be cwd-scoped — verify, §10 item 6).
- **FR-WS-7** — Worktrees: when the workspace is a git repository, every kind defaults to `--worktree` isolation (FR-SAFE-2), with a per-job **run in place** option (`run_in_place`) for when local, untracked files (`CLAUDE.local.md`, `.claude/settings.local.json`, `.env`) or direct edits to the real checkout are wanted. A non-git workspace runs in place; the UI warns that nothing isolates it. The UI states plainly that a worktree contains only tracked files.
- **FR-WS-8** — Extra folders (`add_dirs`, passed as `--add-dir`) are available on every kind via an "Add folder" control. Because every kind can now write (FR-SAFE-3), the UI marks them as read-write.

**Attachments**

- **FR-ATT-1** — The composer supports attaching files via a button/file picker, drag-and-drop, and **pasting from the clipboard** (pasted images become `screenshot-<timestamp>.png`). Attached items appear as removable chips, with a thumbnail for images.
- **FR-ATT-2** — Attachments are **snapshotted**: copied into daemon-owned storage (`<data dir>/jobs/<job-id>/attachments/`) at queue time, so a job queued for overnight still has its files if the originals are moved or deleted. Browsers upload via `POST /api/attachments` (staging, returns an id that job creation references).
- **FR-ATT-3** — The runner appends an "Attached files" block to the prompt listing each file's absolute path and name, so Claude knows they exist; images and PDFs are read with the `Read` tool.
- **FR-ATT-4** — Defaults (configurable): 25 MB per file, 100 MB and 20 files per job; exceeding them is a clear error, not a silent drop.
- **FR-ATT-5** — Retention: staged-but-unused uploads are removed after 24 h; a job's attachments are removed with the job and after a configurable period (default 30 days) once it is terminal.
- **FR-ATT-6** — CLI parity: `tokenwarden queue add --attach <path>` (repeatable); the daemon performs the same snapshot.
- **FR-ATT-7** — Attachments are stored with owner-only permissions. Logs record names and sizes only, never contents.
- **FR-ATT-8** — Job detail shows a job's attachments (thumbnails for images).

**Output folder**

- **FR-OUT-1** — `output_dir` is optional on every kind. When set, the runner grants write access to it (`--add-dir`) and tells Claude in the prompt to save generated files there (absolute path) and not to overwrite existing files. The no-overwrite instruction is prompt-level, not enforced.
- **FR-OUT-2** — When unset, deliverables stay in the workspace (or its worktree); job detail shows where that is.
- **FR-OUT-3** — Where feasible the job record lists files created in `output_dir` (directory listing before vs. after the run); full artifact browsing/diffing remains FR-ART-1.

**UI**

- **FR-UI-1** — The Create Job composer exposes workspace (with picker), attachments, extra folders, output folder, and run-in-place, in addition to kind/model/effort/priority. CLI and API have the same fields (`--workspace`, `--attach`, `--add-dir`, `--output-dir`, `--run-in-place`).
- **FR-UI-2** — Before queuing, the UI summarises what will happen: effective permission posture (FR-SAFE-3), worktree vs. in place, what project context was found in the workspace (FR-WS-4), and a warning if the workspace contains hooks or MCP servers (FR-SAFE-6).

---

## 6. The pacing engine

### 6.1 Sensor fusion

Because ground truth is intermittent (§3.3), window state is fused from three sources:

1. **Ground truth** — `rate_limits` captured by the `twprobe` statusline shim. Two capture paths:
   - **Passive:** shim installed globally in `~/.claude/settings.json`, so the user's own interactive sessions feed the daemon for free.
   - **Active:** a periodic **PTY sentinel** — a short haiku-model session that costs roughly $0.02 and is rate-limited (e.g. at most once per 15 minutes, or after every N jobs). Negligible against a weekly budget, and validated by SPIKE-001.
2. **Local ledger** — every dispatched run's `result` message yields exact token counts and cost. Entries age out of rolling 5-hour and 7-day buckets.
3. **Calibration** — pairs of (Δ`used_percentage`, tokens spent) are fit to learn the plan's tokens-per-percent, converting the ledger into predicted window fill and enabling pre-dispatch cost estimation for queued jobs.

Every ground-truth reading **snaps** the estimate and updates the calibration residual. Confidence decays with time since the last fix; low confidence makes the engine more conservative automatically.

### 6.2 Dispatch loop

```
tick (every 30s):
  1. refresh usage state (ground truth if fresh, else ledger + calibration)
  2. if inside a reserved block                  → dispatch nothing
  3. if five_hour.used ≥ ceiling(aggressiveness) → sleep until resets_at
  4. if seven_day.used ≥ weekly_target           → idle until weekly reset
  5. compute burn rate needed to hit weekly_target by seven_day.resets_at,
     across remaining non-reserved hours
  6. if actual burn < needed → widen concurrency / allow larger jobs
     if actual burn > needed → throttle
  7. pick highest-priority job; if predicted cost exceeds remaining 5h
     headroom, attempt to fit it (§6.4); if it cannot fit, defer and
     try the next-best job
  8. dispatch; stream events to UI; append to ledger on completion
```

### 6.3 Precision constraint

`used_percentage` has **1% resolution** (§3.2). On a Max 20× plan a single percent is a substantial amount of work, so the engine must not treat individual readings as precise. Consequences:

- Calibration fits across **many** observations, never a single delta.
- Safety margin is at least 2× the quantisation error (≥2% headroom below any ceiling).
- Dead-reckoned estimates carry an explicit uncertainty band, and the UI shows the band, not a false-precision number.

### 6.4 Fitting oversized jobs

When the top-priority job's predicted cost exceeds remaining 5-hour headroom, the engine tries, in order:

1. **Budget-capped continuation (preferred).** Dispatch with `--max-budget-usd` set to remaining headroom; the run stops at the cap and resumes next window via `--resume <session_id>`. Full context carries across windows — no decomposition error, no lost work.
2. **Declared steps.** If the job carries user-authored `Steps`, promote each to a child job sharing the parent's session and workspace, and dispatch as many as fit. Deterministic and free; the queue UI should encourage steps on large jobs.
3. **Model-driven decomposition (last resort).** A cheap `--model haiku --json-schema` call proposes ordered independent subtasks. Gated: only when 1 and 2 do not apply, only when predicted savings exceed the decomposition cost, and cached on the job so it is never paid for twice.
4. **Defer.** Mark `deferred_oversized`, record the reason, move to the next-best job. If the job would not fit even a full empty window, surface it in the UI as needing manual splitting — silently retrying forever is the failure mode to avoid.

**Guardrail:** partial runs must never leave a workspace broken. Interruptible jobs default to `--worktree` isolation, and continuation-capable jobs get an appended instruction to leave a short `PROGRESS.md` before stopping.

---

## 7. Non-functional requirements

- **NFR-PLAT-1** — Supports macOS (arm64, amd64), Linux (amd64, arm64), and Windows (amd64).
- **NFR-PERF-1** — Idle daemon: <50 MB RSS, <1% CPU. Desktop shell adds <100 MB via the OS-native webview (no bundled Chromium).
- **NFR-PERF-2** — Installed size under 50 MB per platform.
- **NFR-PERF-3** — `twprobe` must start in <5 ms and never block a Claude turn: read stdin, fire-and-forget over the local socket, print one line, exit. It sits in the latency path of every status line render.
- **NFR-BUILD-1** — **No cgo.** SQLite via `modernc.org/sqlite`, so `GOOS=windows go build` works from a Mac with no C toolchain. This is what keeps the CD pipeline cheap and is a hard constraint on dependency choice.
- **NFR-UI-1** — Light and dark themes, following the OS preference by default with an explicit override.
- **NFR-UI-2** — The UI is a local web app served by the daemon; the desktop window is a thin shell over it. Consequences: the app is usable from a phone or another machine on the LAN (off by default), and the shell is replaceable without touching application code. Exception (revision 2): the native folder picker (FR-WS-5) needs a minimal shell-provided bridge; the UI feature-detects it and works without it (typed path).
- **NFR-MAINT-1** — Clear module boundaries (`scheduler`, `budget`, `runner`, `queue`, `store`, `api`, `integrations`) with dependencies pointing inward. New job kinds and work sources are added by implementing an interface, not by editing the scheduler.
- **NFR-TEST-1** — Integration tests run against a **fake `claude` binary** that emits scripted `stream-json` (including rate-limit errors and retry events). The real dispatch path is exercised with **zero tokens spent and no network**, which is what makes CI meaningful rather than decorative.
- **NFR-TEST-2** — Simulated-clock tests assert that a simulated week lands within tolerance of the aggressiveness target across scenarios: empty queue, oversubscribed queue, mid-week reserved block, injected rate-limit errors, and an oversized job capped-and-resumed across three windows.
- **NFR-CI-1** — GitHub Actions CI on every push/PR: matrix over {ubuntu, macos, windows}, `go vet`, `golangci-lint`, `go test -race ./...`, UI build and test, `govulncheck`.
- **NFR-CD-1** — GitHub Actions release on `v*` tags: cross-compiled binaries for all five target triples via goreleaser, per-platform desktop shell builds, checksums and generated changelog attached to a GitHub Release.
- **NFR-DOC-1** — User documentation (getting started, scheduling, troubleshooting) and developer documentation (architecture, pacing algorithm, contributing, local development).

---

## 8. Safety and trust

- **NFR-SEC-1** — No credentials handled, stored, or transmitted (§4.2).
- **NFR-SEC-2** — The daemon binds to `127.0.0.1` only. LAN exposure is opt-in and requires an explicit token.
- **FR-SAFE-1** — `--dangerously-skip-permissions` is never used. Not as a default, not as an option.
- **FR-SAFE-2** — Unattended jobs default to `--worktree` isolation (when the workspace is a git repo; per-job `run_in_place` opt-out, FR-WS-7) so nothing lands on a working branch overnight, plus a per-job `--max-budget-usd`, enforced across a job's whole lifetime (`UserMaxBudgetUSD`). Unattended combined with permissive is precisely how an overnight run becomes a bad morning.
- **FR-SAFE-3** — Autonomy posture is per job kind, and visible in the UI before a job is queued (FR-UI-2). **Revision 2 (2026-10-05):** every kind may use project skills, and the user chose to give every kind the full tool set rather than keep research/plan/review read-only. Kinds are now presets for permission mode:

  | Kind | Permission mode | Tools | Filesystem |
  |---|---|---|---|
  | `research` | `dontAsk` | full set + `Skill` | `--worktree` by default (FR-WS-7) |
  | `plan` | `plan` (Claude Code's own propose-don't-edit mode) | full set + `Skill`; the mode itself blocks edits | none needed |
  | `code` | `acceptEdits` | full set + `Skill` | `--worktree` by default |
  | `review` | `dontAsk` | full set + `Skill` | `--worktree` by default |
  | `freeform` | user-specified | user-specified (`Skill` must be included unless the user lists tools explicitly) | user-specified |

  "Full set" = `Read`, `Write`, `Edit`, `Bash`, `Grep`, `Glob`, `WebFetch`, `WebSearch`, `NotebookRead`/`NotebookEdit`, `Skill` — exact tool names and `--allowedTools` syntax to be confirmed against the real CLI (§10 item 5). The `plan` row is an **assumption** made when writing this revision (the user said "all modes"; `plan` keeping its read-only mode seemed the sensible reading) — confirm with the user before implementing.

  This supersedes the earlier rule that a fixed kind's posture could not be widened. The invariants that remain non-negotiable: FR-SAFE-1 (no `bypassPermissions`, anywhere), FR-SAFE-2 (worktree default + lifetime budget cap), FR-SAFE-4 (kill switch), FR-SAFE-5, and the audit trail in FR-SAFE-7. Unattended + `Bash` is powerful; the mitigations are those four things, not a tool restriction.

- **FR-SAFE-4** — A global kill switch stops all dispatch immediately and terminates running subprocesses.
- **FR-SAFE-6** — Project hooks and MCP servers in the workspace (`.claude/settings*.json` hooks, `.mcp.json`) are allowed to run, as they would in a normal session. Dispatch detects and **logs** them (a `workspace_context` line: CLAUDE.md, skill count, hooks, MCP) and the UI **warns** before queuing (FR-UI-2). Not blocked, not opt-in — the user chose visibility over restriction.
- **FR-SAFE-7** — Audit trail: the job log records each tool invocation (tool name; for `Bash`, the command truncated to 200 chars) from the `stream-json` events, so an overnight run's actions can be reviewed from the log alone.
- **FR-SAFE-5** — tokenwarden schedules only work the user has queued or explicitly approved from a work source. It never invents work (§1.1).

---

## 9. Out of scope for v1

- Integration providers (GitHub, ClickUp, Google Drive) — interface only, no implementations.
- Cowork sessions and Claude Code Agent Teams as job kinds.
- Multi-account and team support.
- Cloud-hosted scheduling (Anthropic Routines / GitHub Actions dispatch).
- Mobile-native clients (the responsive web UI is the mobile story).
- macOS notarization and Windows code signing — deferred, with unsigned-install instructions documented rather than pretended away.
- **Sentinel cadence strategy selection.** v1 ships the PTY sentinel on a fixed wall-clock interval only (§10 item 1). Letting the user choose between fixed-time and fixed-job-count cadence (fire every N dispatched jobs instead of every N minutes) is deferred to a later release.
- **Stretch goal, post-v1: adaptive sentinel cadence.** Vary sentinel frequency by confidence (time since last reading, size of an upcoming dispatch) instead of a fixed interval — the most accurate option, but only worth building once real burn-in data from the fixed-interval version exists to tune it against.

---

## 10. Open questions

1. ~~**Sentinel cadence.**~~ **Resolved:** fixed 30-minute interval for v1. Strategy selection (fixed-time vs. fixed-job-count) and adaptive cadence are deferred — see §9.
2. **Concurrency.** Do parallel `claude -p` runs give better window utilisation, or do they mainly cause overshoot past the ceiling? Measure before enabling.
3. ~~**Cost estimation cold start.**~~ **Resolved:** no prior. Until `internal/budget.CalibrateFiveHour`/`CalibrateSevenDay` report `Insufficient: false` (≥3 samples), the engine dispatches one job at a time and relies solely on the hard ceiling check (§6.2 step 3) rather than a predicted-cost fit.
4. **Weekly model-specific caps.** Max plans reportedly carry a second weekly limit scoped to certain models. `rate_limits` exposes only `five_hour` and `seven_day`, so a model-specific cap may be invisible to the engine and only discoverable via an error. Needs investigation.
5. **Skill tool names and allowlist syntax.** Confirm in the real CLI, in `-p` mode, what the skill tool is called, how it appears in `--allowedTools`, and that project skills load from the workspace (SPIKE-002).
6. **Resume vs. cwd.** Is `--resume` scoped to the cwd/worktree it was created in? Determines FR-WS-6 and how worktree-per-run interacts with resumable jobs.
7. **Cost prediction with project context.** `CLAUDE.md` and skills inflate every run's context. `PredictJobCost` is a per-kind mean; should it key on (kind, workspace)?
8. **Concurrency per workspace.** When §10 item 2 lands, two in-place jobs in one workspace must not run simultaneously.
9. **Worktree clutter.** Defaulting every kind to `--worktree` creates a worktree per run (the overnight test already produced 100+). Verify Claude Code removes unchanged ones; otherwise add cleanup.
10. **macOS folder permissions (TCC).** First access to Documents/Desktop/OneDrive folders prompts; confirm the prompt is attributed to the app when the daemon is its child, and that a daemon started from a terminal behaves.
11. **Shell bridge vs. NFR-UI-2.** How the web UI asks the desktop shell for a native folder dialog (Wails dialog API) without making the shell load-bearing.
12. **Folder "attachments".** Dropping a folder means "add as extra directory", not "copy it" (FR-WS-8); decide whether to also offer a copy-snapshot for huge or volatile folders.

---

## 11. Verification

- Unit and integration suites green on all three platforms in CI.
- Simulated-week pacing tests within tolerance across all scenarios in NFR-TEST-2.
- Analytics reconciliation: indexed transcript totals match the figures shown by `/usage` in a live session.
- **Burn-in:** queue low-stakes research jobs overnight at 60% aggressiveness; next morning compare predicted weekly usage against a real `/usage` reading. Prediction error is the headline quality metric and belongs on the dashboard.
- `go build` cross-compiles for all five target triples from a Mac with no C toolchain, proving the no-cgo constraint held.

---

## 12. Compliance note

tokenwarden automates the user's own Claude Code CLI, on their own machine, under their own subscription, for their own work. It does not resell access, proxy claude.ai authentication to third parties, or expose the user's rate limits to anyone else — all of which Anthropic's Agent SDK documentation prohibits. It handles no credentials.

The design targets real queued work rather than synthetic load (§1.1, FR-SAFE-5). Users remain responsible for their own use complying with Anthropic's Terms of Service, and the README states this plainly.
