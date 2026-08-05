-- The local usage ledger (REQUIREMENTS.md §6.1 item 2): one row per
-- (job, model) the runner reported cost/tokens for. This is exact spend
-- tokenwarden itself dispatched — it is not the plan's actual rate-limit
-- window fill, which needs ground truth (the future twprobe/PTY sentinel)
-- and calibration to estimate. See internal/budget.
CREATE TABLE usage_entries (
    id                          TEXT PRIMARY KEY,
    job_id                      TEXT NOT NULL,
    model                       TEXT NOT NULL DEFAULT '', -- empty when the CLI didn't report per-model usage
    input_tokens                INTEGER NOT NULL DEFAULT 0,
    cache_creation_input_tokens INTEGER NOT NULL DEFAULT 0,
    cache_read_input_tokens     INTEGER NOT NULL DEFAULT 0,
    output_tokens               INTEGER NOT NULL DEFAULT 0,
    cost_usd                    REAL NOT NULL DEFAULT 0,
    recorded_at                 INTEGER NOT NULL -- unix seconds
);

CREATE INDEX idx_usage_entries_recorded_at ON usage_entries(recorded_at);
CREATE INDEX idx_usage_entries_job_id ON usage_entries(job_id);
