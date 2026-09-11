# Using subagents on this project: what worked, what to watch for

Written after building REQUIREMENTS.md §6.4 strategy 2 (declared-steps
promotion) with an explicit implement → (test ‖ review) → fix pipeline, on
user instruction to use subagents for implementation, testing, code review,
and to coordinate them as a "master agent." This is the playbook for
repeating that on the next nontrivial slice, plus what went wrong along the
way.

## When this is worth doing

Not every change. The same session that ran the full pipeline below also
made a ~20-line CI workflow addition (a new cross-platform build job)
directly, no subagents — a mechanical, low-risk, single-file change doesn't
need three agent round-trips to be trustworthy, and the overhead would have
been pure waste. Reserve the pipeline for changes with real correctness
surface: new schema, new state machine transitions, anything that touches
existing safety/permission logic, anything where "it compiles and the happy
path works" is not the same claim as "it's correct."

## The pipeline that worked

1. **Coordinator (you) reads the real code first.** Before writing a single
   subagent prompt, read the actual current files you're about to touch —
   not a summary, the files. For strategy 2 this meant reading `store/job.go`,
   `queue/queue.go` (`Enqueue`, `computeStatus`, `DeferOversized`,
   `CapBudget`, and the then-uncalled `PromoteReady`), `scheduler/policy.go`
   (`FitJob`), and `scheduler/engine.go` (`Engine.Tick`) end to end, plus the
   exact migration-file style and DTO-mirroring convention. This is what
   "never delegate understanding" means in practice: an implementer prompt
   that names exact function signatures, exact existing patterns to copy
   (error-sentinel style, doc-comment voice, test-file conventions), and the
   exact mechanics you've already worked out (how the dependency chain
   should be built, when a session ID should propagate) leaves little room
   for the agent to invent a different, wrong design. A vaguer prompt
   ("implement strategy 2 from the requirements doc") would still produce
   something that compiles — it would not produce something that matches
   this codebase's conventions or gets the subtle parts right, and you'd
   have to catch that in review anyway, at which point you've paid for two
   passes instead of doing the thinking once, up front.

2. **One implementer agent, `model: opus`, foreground.** Ran it in the
   foreground (not backgrounded) because the next step (test + review)
   depends on its output — there was nothing else useful to do in parallel.
   Gave it the full design (every field to add, every function signature,
   which existing test files to model new tests on) and explicitly told it
   what *not* to build (strategy 3, any UI for triggering promotion
   manually) so it wouldn't scope-creep. Told it not to write tests itself —
   that's the next agents' job — only to keep the *existing* suite green.

3. **Tester and reviewer agents, in parallel, against the same finished
   implementation.** This is the part worth repeating deliberately: **a
   tester and a reviewer are not redundant with each other, and running only
   one is a real coverage gap.** The tester's job was "does this do what it
   was designed to do" — it wrote real tests (chain construction, session
   hand-off, dispatch ordering, simulated-clock `Engine.Tick` runs against
   the fake `claude` binary), ran them, and reported back: everything
   passed, zero bugs found. The reviewer's job was "is what was built
   actually correct and safe, independent of whether it does what it was
   told to do" — reading the diff against this codebase's safety invariants
   (FR-SAFE-2/3) and its dependency-cascade semantics (`computeStatus`,
   `Status.Terminal()`), *not* against the implementer's own stated intent.
   It found five real bugs the tester's tests never would have caught,
   because none of them fail a "does `PromoteSteps` do what I designed"
   test — they only surface when someone asks "does this violate an
   invariant the rest of the codebase depends on":
   - Children silently dropped the parent's `AllowedTools`/`PermissionMode`/
     `MaxBudgetUSD`/etc., which — because of how `internal/runner/safety.go`
     treats an *empty* field as "apply the kind's default" rather than
     "inherit nothing" — silently widened what an unattended step could do
     (a real FR-SAFE-3 violation, not a style nit).
   - Promotion wasn't idempotent: a partial failure retried on a later tick
     would re-promote the same job and duplicate real token spend, forever.
   - Promoting a job silently cancelled *unrelated* jobs that happened to
     depend on it, with a misleading "dependency failed" error, because the
     new terminal status wasn't special-cased in the existing
     failed-dependency cascade.
   - The first child didn't inherit an already-existing parent session,
     contradicting the requirements doc's literal wording.
   - One permanently-broken blocked job could wedge the *entire scheduler*
     forever, because a new per-tick call didn't tolerate the kind of
     partial failure the function it was calling was explicitly designed to
     tolerate.

   Both agents ran with `run_in_background: true` so the turn could return
   control immediately; results arrived as separate task-notification
   events, not in the same turn. Do not fabricate or guess at a
   backgrounded agent's result before its notification arrives — there's
   nothing to predict from.

4. **A reviewer needs to be told to (a) read the actual current files, not
   a description of them, and (b) run the verification commands itself.**
   Both instructions were explicit in the reviewer's prompt, and both
   mattered: it independently ran `go build`/`vet`/`test -race`/`task lint`
   rather than trusting the implementer's or tester's self-report, which is
   good practice regardless of whether anyone actually misreported anything
   — an agent's summary describes what it *intended* to do, not necessarily
   what it did, the same caution that applies to trusting your own subagents'
   reports back to you.

5. **The coordinator synthesizes findings into a precise fix spec — don't
   just forward the review.** The reviewer's report was handed to a fresh
   fix-pass agent, but not verbatim: each finding was turned into a numbered
   fix with the actual resolution decision already made (e.g., the reviewer
   raised that forcing children `Resumable: true` inverted the user's
   explicit choice on the parent; the fix spec didn't leave that as an open
   question, it said "inherit `job.Resumable`" and explained why). Leaving a
   genuine design decision to a fix-agent risks it picking a different call
   than the one you, having seen the whole picture, would make — and now
   you have to review *that* judgment call too, which defeats the point of
   having synthesized in the first place.

6. **A good agent will surface a real conflict in your spec instead of
   silently picking a side — but only if you tell it to.** The fix spec
   asked for two things that turned out to be in tension: check for
   existing children *and* preserve the existing terminal-status guard in
   its existing position. The fix agent found the actual conflict (a
   successful promotion leaves the parent terminal, so a same-order repeat
   call would hit the terminal guard before ever reaching the idempotency
   check), picked the correct resolution (check existing children *before*
   the terminal guard), and reported the deviation explicitly rather than
   silently doing something different from what was asked. This only works
   because the prompt said, in so many words, "if you deviate from any
   instruction because the actual code didn't match what's described, say
   exactly what you found and what you did instead." Without that
   instruction, you'd have no way to know two mental models of the same fix
   had diverged.

7. **Spot-check the highest-stakes code yourself before trusting the
   report.** After the fix pass, the two functions the review had flagged
   as safety-critical (`PromoteSteps`, the new `rewireDependents`) were read
   directly, in full, by the coordinator — not just the agent's summary of
   them. This is cheap (a few minutes) relative to the cost of shipping a
   subtle regression in exactly the code that was just identified as the
   risky part.

## What this pipeline cannot catch

Everything above ran, and could only run, on the machine actually available
— this Mac. The very next piece of work (a CI job proving the separate
`desktop/` Wails module compiles on Linux and Windows too) hit a real,
Linux-specific build failure — Wails v3 defaults to a GTK4/WebKitGTK-6.0
backend that isn't installed by the package set the general Wails
documentation had suggested; the actual fix (`-tags gtk3` plus the matching
GTK3/WebKit2GTK-4.1 packages) only became clear from reading the real
`pkg-config` error in a real CI run. No amount of local implement/test/
review on macOS could have caught this, because the bug wasn't in the logic
at all — it was a build-time dependency mismatch specific to a platform
nothing in the pipeline had access to. **For genuinely cross-platform
changes, a real CI run on the target platform is not optional or redundant
with local agent verification — budget for it as its own review pass, and
expect the first run to fail on something only that platform would ever
reveal.**

Relatedly: this whole pipeline is safe to run semi-autonomously specifically
*because* the feature it built is free and deterministic — every dispatch
in every test went through the fake `claude` binary (`internal/runner/
testdata/fakeclaude`, per this repo's own `NFR-TEST-1`), enforced by an
explicit "never shell out to the real `claude` CLI" instruction in every
subagent prompt. §6.4 strategy 3 (model-driven decomposition) breaks that
invariant — it's a real `--model haiku` call with real cost, gated by a
cost-benefit heuristic REQUIREMENTS.md doesn't actually define
("predicted savings exceed the decomposition cost" — savings compared to
what, exactly, isn't specified). That ambiguity plus the real spend is
exactly why it was *not* handed to a subagent pipeline and instead flagged
as needing a design decision first. **Rule of thumb: this pipeline is for
well-specified, zero-marginal-cost changes. Anything that spends real money
or has an irreversible external side effect needs the ambiguous parts
resolved by a human before any implementation agent gets involved — review
after the fact is not a substitute for that.**

## A process mistake worth flagging

Partway through this session, a new work branch got created while an
*earlier, still-unmerged* PR's branch was still checked out, instead of off
`main` — a `git checkout main` had silently failed (uncommitted changes in
the way) and the subsequent `git checkout -b` succeeded anyway, off the
wrong parent. It was caught before pushing, by comparing `git log --oneline
-3` against `git log --oneline main -3` and noticing the new branch's
history included a commit that wasn't on `main` yet — but untangling it cost
several extra commands (stash → checkout main → checkout the new branch off
a *clean* main → pop the stash). **Concrete rule: after `git checkout -b
<name>`, before making any commits, verify `git log --oneline -1` on the new
branch matches `git log --oneline -1 origin/main` — especially if any other
branch might still have been checked out a moment ago.**
