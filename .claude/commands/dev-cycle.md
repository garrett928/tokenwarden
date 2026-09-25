---
description: Run one autonomous backlog item end-to-end (implement, test, review, fix, merge) with no human checkpoint.
---

Read [`docs/AUTONOMOUS-DEV-LOOP.md`](../../docs/AUTONOMOUS-DEV-LOOP.md) and
[`docs/SUBAGENT-WORKFLOW.md`](../../docs/SUBAGENT-WORKFLOW.md) in full before
doing anything else, even if you've read them in a prior cycle — they are the
actual spec for this command, not background color. This command is meant to
be invoked repeatedly (typically via `/loop dev-cycle`) with no human
reviewing output between cycles, so follow both documents exactly rather than
improvising around them.

Then, in order:

1. **Preflight.** `git status` (must be clean — if not, stop:
   `STOPPED: dirty working tree`). `git checkout main && git pull`. Confirm
   `gh auth status` and that `task build`/`task test`/`task lint` all pass on
   `main` right now before touching anything — if `main` itself is broken,
   stop: `STOPPED: main is broken before any change`.

2. **Pick one backlog item** from CLAUDE.md's "Handoff" → "Backlog" section:
   the first entry that isn't waiting on something external (an open PR, a
   platform nobody here can test). If the list is empty or every remaining
   entry is externally blocked, end your turn with exactly:
   `BACKLOG EMPTY`

3. **Classify it** per `AUTONOMOUS-DEV-LOOP.md`'s risk tiers (mechanical vs.
   real correctness surface) and, if it's one of the two policy-ambiguity
   items called out in that doc, follow its "Blocked / ambiguity items"
   section (Opus decision-record step first, flag-defaulted-off for any
   real-money code path) before proceeding to implementation.

4. **Branch.** `git checkout -b <descriptive-name>` off the `main` you just
   pulled. Verify `git log --oneline -1` matches
   `git log --oneline -1 origin/main` before your first commit.

5. **Run the pipeline** for that item's tier, exactly as
   `AUTONOMOUS-DEV-LOOP.md` describes it — model assignment (Sonnet default,
   Opus only for the reviewer step / decision-record step) is not a
   suggestion, it's the cost control for this whole exercise. Do not spawn a
   tester or reviewer subagent for a mechanical change, and do not upgrade a
   step to Opus because an item feels hard.

6. **Verify.** `task build && task test && task lint` (plus
   `task ui:build`/`task ui:lint` if `ui/` changed) must be green before you
   commit. If they aren't after your fix pass, stop:
   `STOPPED: verification failing on <item>` — do not force a commit through.

7. **Update CLAUDE.md** yourself: cross the item off the Handoff backlog,
   bump "Last updated" to today, note what shipped. This is real project
   state and nobody else will update it.

8. **Commit, push, open the PR** (`gh pr create`), then **immediately**
   (before checking CI at all) call `ccd_pr`'s `set_auto_merge(enabled:
   true)` — this is the one standing exception to "ask before enabling
   auto-merge" documented in `AUTONOMOUS-DEV-LOOP.md`; don't ask for
   confirmation, it was already given for this specific loop. Calling it
   *after* confirming green CI doesn't work: GitHub's API refuses
   `set_auto_merge` on a PR that's already fully mergeable ("Pull request is
   in clean status") — it only attaches to a PR with something still
   pending. If it instead fails with "Auto merge is not allowed for this
   repository," the repo's own "Allow auto-merge" setting is off — enable it
   once (`gh api repos/<owner>/<repo> -X PATCH -f allow_auto_merge=true`)
   and retry; this is a one-time repo setting, not per-PR.

9. **Use `ccd_pr`'s `bind_pr`/`get_status` to read CI** — never poll with raw
   `gh` commands or your own sleep loop. Auto-merge doesn't need this to
   actually merge (GitHub does that on its own once checks pass), but you
   still need to know whether it happened or CI failed. On red CI: one
   Sonnet fix attempt against the actual failure log, push, recheck once.
   Still red: call `set_auto_merge(enabled: false)` (so a later unrelated
   push doesn't trigger a surprise merge) and stop:
   `STOPPED: CI failing after one fix attempt on PR #<n>`.

10. End your turn with exactly one status line, nothing else after it:
    `ITERATION COMPLETE: <one-line summary of what shipped, PR link>`
    or one of the `BACKLOG EMPTY` / `STOPPED: <reason>` forms above.

Do not do a second backlog item in the same invocation, and do not schedule
your own next wakeup — that's the wrapping `/loop` session's job, driven by
the status line you end on.
