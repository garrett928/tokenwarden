-- The runner's final text output (REQUIREMENTS.md FR-ART-3: "full job
-- history with prompt, flags used, token cost, duration, and outcome").
-- Empty until the job completes; set on both success and failure since
-- claude's own error summary is useful diagnostic text either way.
ALTER TABLE jobs ADD COLUMN result_text TEXT NOT NULL DEFAULT '';
