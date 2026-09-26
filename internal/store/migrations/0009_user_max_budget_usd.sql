-- user_max_budget_usd records what the user actually asked for at job
-- creation, immutable afterward -- distinct from max_budget_usd, which is
-- the current *effective* per-dispatch cap that queue.CapBudget (§6.4
-- strategy 1) may tighten to fit a specific window's remaining headroom.
-- See internal/store/job.go's Job.UserMaxBudgetUSD doc comment for the real
-- incident this distinction closes: a cumulative-lifetime-spend check must
-- compare against the user's real, stable intent, never a scheduler pacing
-- value that can go stale as headroom changes tick to tick.
ALTER TABLE jobs ADD COLUMN user_max_budget_usd REAL;
