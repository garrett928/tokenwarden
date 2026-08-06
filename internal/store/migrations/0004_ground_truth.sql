-- Ground-truth rate_limits readings (REQUIREMENTS.md §6.1 item 1): captured
-- by the twprobe statusline shim from the user's own interactive sessions
-- (passive path) or, in a future slice, a periodic PTY sentinel (active
-- path). used_percentage is an integer (SPIKE-001 conclusion 4, §6.3) —
-- 1% resolution, not a float.
CREATE TABLE ground_truth_readings (
    id                          TEXT PRIMARY KEY,
    five_hour_used_percentage  INTEGER NOT NULL,
    five_hour_resets_at        INTEGER NOT NULL, -- unix seconds
    seven_day_used_percentage  INTEGER NOT NULL,
    seven_day_resets_at        INTEGER NOT NULL, -- unix seconds
    observed_at                 INTEGER NOT NULL -- unix seconds, when tokenwarden ingested this reading
);

CREATE INDEX idx_ground_truth_readings_observed_at ON ground_truth_readings(observed_at);
