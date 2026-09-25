# The autonomous dev loop

An unattended extension of the pipeline in
[`SUBAGENT-WORKFLOW.md`](SUBAGENT-WORKFLOW.md): same implement → (test ‖
review) → fix shape, but run repeatedly with nobody watching, merging its
own PRs, and picking its own backlog item each time. Read
`SUBAGENT-WORKFLOW.md` first — this document only covers what's different
when there's no human checkpoint at the end.

**Two guiding principles, non-negotiable:**

1. **No human touches code.** Every change goes implementer → reviewer →
   merge with nobody reading a diff in between. That makes the review step
   and the guardrails below load-bearing in a way they weren't when a human
   was the last line of defense.
2. **Cost-effective.** Opus is not the default model for anything. It is
   spent deliberately, on the one or two steps that have shown they catch
   real bugs the cheap steps miss.

## How it's invoked

This is **not** a tokenwarden job, and it does not run through
tokenwardend's own scheduler — it builds tokenwarden, it isn't one of the
budget-aware workloads tokenwarden schedules for someone else. It only runs
as a Claude Code session with this repo as its working directory:

```
/loop dev-cycle
```

`/dev-cycle` (see `.claude/commands/dev-cycle.md`) does exactly one backlog
item end-to-end and then ends its turn with a one-line status. The `/loop`
skill wraps that: it re-invokes `/dev-cycle` on a self-paced schedule
(`ScheduleWakeup`), reading that status line to decide whether to keep
going. `/dev-cycle` never schedules its own wakeups or polls CI in a loop —
that's the wrapping `/loop` session's job, using the `ccd_pr` tools for CI
status rather than hand-rolled polling.

Three status lines `/dev-cycle` can end on, and what the wrapping loop does
with each:

- `ITERATION COMPLETE: <summary>` — merged (or cleanly deferred, see
  "Oversized/blocked items" below) with nothing left broken. Schedule the
  next wakeup and go again.
- `BACKLOG EMPTY` — nothing actionable left in CLAUDE.md's Handoff backlog.
  Stop the loop (`stop: true`). Don't poll an empty backlog forever.
- `STOPPED: <reason>` — a guardrail tripped (see below). Stop the loop and
  leave things exactly as they are for a human to look at next time someone
  opens this repo. Do not retry automatically.

## Backlog and bookkeeping

The backlog is CLAUDE.md's existing "Handoff" section — no separate tracker
to invent or keep in sync. Each cycle:

1. Reads the numbered backlog list, picks the first item that isn't blocked
   on something external (a still-open PR, a platform nobody can test
   locally, etc.).
2. At the end of a successful cycle, edits CLAUDE.md itself: removes or
   checks off the finished item, updates the "Last updated" date, and notes
   the PR/commit. Nobody else is going to do this bookkeeping, so the loop
   must.

## Risk-tiered pipeline depth

Reuse `SUBAGENT-WORKFLOW.md`'s own triage line verbatim: *"a mechanical,
low-risk, single-file change doesn't need three agent round-trips."* Two
tiers:

- **Mechanical / low-risk** (docs, CI config, Taskfile plumbing, anything
  with no state-machine or safety-code surface): the coordinator either
  makes the change directly, or spawns a single Sonnet implementer followed
  by a single Sonnet review pass. No tester agent, no Opus, no parallel
  fan-out — the overhead isn't earned.
- **Real correctness surface** (new schema, new state transitions, anything
  touching `internal/runner/safety.go`, `internal/queue`'s status
  cascade, or budget/permission logic): the full pipeline below.

Getting the tier wrong in the cheap direction is the failure mode to guard
against — when in doubt, treat it as real correctness surface. The cost of
an unnecessary review pass is small; the cost of an unreviewed safety bug
merging itself is not.

## The full pipeline (real correctness surface)

Model assignment is fixed and is the primary cost lever — do not upgrade a
step to Opus because a particular item "feels hard"; upgrade it only by
changing this document.

1. **Coordinator reads the real code first**, exactly as
   `SUBAGENT-WORKFLOW.md` §1 describes — not a summary. This step has no
   subagent because delegating it defeats the point: an implementer prompt
   is only as good as the design decisions already made before it's
   written.
2. **Implementer — Sonnet, foreground.** Given the exact design (field
   names, function signatures, which existing files to model new code on),
   and told explicitly what *not* to build, same as before. Does not write
   its own tests.
3. **Tester (Sonnet) and reviewer (Opus), in parallel, backgrounded.**
   This is the one step that gets the expensive model, because
   `SUBAGENT-WORKFLOW.md`'s own case study found the reviewer catches
   invariant violations — silently widened permissions, broken idempotency,
   wrong dependency-cascade behavior — that a passing test suite does not
   surface, because they're not failures of "does it do what it was
   designed to do." That gap is exactly what no longer has a human backstop,
   so it's the one place spending more is worth it. Both agents are told to
   independently run `task build`/`task test`/`task lint` themselves, not
   trust the implementer's self-report.
4. **Coordinator synthesizes a fix spec** — turns the reviewer's findings
   into numbered fixes with the actual resolution decision already made,
   not forwarded verbatim. Leaving a design call open for the fix agent
   means re-reviewing its judgment too, which spends more than just making
   the call up front.
5. **Fix agent — Sonnet.** Applies the spec. Explicitly told: if the actual
   code doesn't match what the spec assumed, say exactly what was found and
   what was done instead, rather than silently picking a side.
6. **Coordinator spot-checks the highest-stakes changed functions directly**
   — reads the actual diff of whatever the reviewer flagged as
   safety-critical, not just the fix agent's summary of it. Cheap relative
   to shipping a regression in exactly the code just identified as risky.
7. **Full verification**, coordinator's own responsibility, not delegated:
   `task build`, `task test`, `task lint`, plus `task ui:build`/`ui:lint` if
   anything under `ui/` changed. Must be green before step 8.

## Git, PR, and merge mechanics

- Before branching: verify the working tree is clean, `git pull` on `main`,
  and confirm `git log --oneline -1` on the new branch matches
  `git log --oneline -1 origin/main` before the first commit —
  `SUBAGENT-WORKFLOW.md`'s closing section documents exactly how a branch
  cut from the wrong parent slipped through before.
- Commit, push, open the PR with `gh pr create`, then use the `ccd_pr`
  tools (`bind_pr`, `get_status`) to read CI — never hand-rolled polling via
  `gh pr checks` in a sleep loop, and never `CronCreate`/`ScheduleWakeup`
  from inside `/dev-cycle` itself for this; the wrapping `/loop` session's
  own pacing covers it.
- **Auto-merge is enabled** (`ccd_pr`'s `set_auto_merge`) once CI is green.
  This is the one piece of this loop that's a deliberate, explicit
  exception to "ask before enabling auto-merge every time" — the user
  authorized it specifically for this loop's design, not as a standing
  blanket permission for anything else this project does.
- If CI comes back red: one Sonnet fix attempt against the actual CI log,
  push, re-check. If still red after that one retry, stop
  (`STOPPED: CI failing after one fix attempt on PR #<n>`) rather than
  retrying indefinitely — see "Guardrails" below.

## Blocked / ambiguity items

Two backlog items (§6.4 strategy 3's undefined "savings" heuristic;
preferred windows' undefined "actively try to use" semantics) were
previously flagged as needing a human policy call before any implementation
agent got involved. With no human in the loop, the coordinator is allowed
to make that call itself — but with more ceremony than a normal design
decision, because getting it wrong here isn't a bug fix away, it's a
product decision baked into merged code:

1. **A dedicated decision-record step, Opus, not backgrounded**, before any
   implementer is spawned. It writes its reasoning and the chosen
   definition into the PR description and into the relevant doc (e.g. a new
   subsection of `docs/REQUIREMENTS.md` or a short decision note) — not
   just into a commit message, since that's the only place a person
   reviewing history later will look for *why*.
2. **Real-money code paths ship flag-defaulted-off.** §6.4 strategy 3 calls
   a real `--model haiku` inference on every trigger — implement it, but
   gate it behind a config flag that defaults to disabled. Deciding what
   "savings" means is a design call the loop is now allowed to make; quietly
   turning on real, repeated spend with nobody watching is not something
   that same allowance should cover. The user opts it on later, deliberately.
3. Everything else about these items (predicted correctness surface, review
   depth) follows the normal "real correctness surface" tier above — if
   anything, err toward more scrutiny, not less, given point 1.

## Guardrails / stop conditions

Any of these ends the loop (`STOPPED: <reason>`) rather than retrying:

- A Hard Constraint from CLAUDE.md would be violated (cgo introduced,
  any credential handling, any `--dangerously-skip-permissions` path) —
  checked explicitly by the reviewer every cycle, full pipeline or not.
- CI still red after one fix attempt on the same PR.
- The same backlog item fails its pipeline twice in a row (bad design,
  flaky environment, whatever the cause — burning a third attempt without a
  human looking is exactly the token-burning failure mode the cost
  principle exists to prevent).
- The backlog is empty (`BACKLOG EMPTY`, not a guardrail trip, but the same
  "stop, don't spin" outcome).

A stopped loop is not an error state to work around automatically — it's
the designed handoff point back to whoever next opens a session here. Leave
the branch/PR/notes exactly as they are; don't clean up "for tidiness" on
the way out, since that context is what the next session needs.
