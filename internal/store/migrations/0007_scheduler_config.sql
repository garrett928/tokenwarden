-- Scheduler configuration (REQUIREMENTS.md §5.2 / §6.2): a single-row
-- table holding the dispatch loop's policy knobs. id is pinned to 1 so
-- there is exactly one config, upserted in place rather than versioned —
-- there is only ever one scheduler running against one queue.
CREATE TABLE scheduler_config (
    id                INTEGER PRIMARY KEY CHECK (id = 1),
    enabled           INTEGER NOT NULL DEFAULT 0,
    aggressiveness    INTEGER NOT NULL DEFAULT 50, -- FR-SCHED-1, 0-100
    reserved_blocks   TEXT NOT NULL DEFAULT '[]',  -- JSON []TimeBlock, FR-SCHED-2
    preferred_windows TEXT NOT NULL DEFAULT '[]',  -- JSON []TimeBlock, FR-SCHED-3
    max_budget_usd    REAL,                        -- nullable global safety cap
    updated_at        INTEGER NOT NULL
);
