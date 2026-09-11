-- parent_job_id records which job a child was promoted from
-- (REQUIREMENTS.md §6.4 strategy 2, declared-steps promotion): the parent
-- goes to status 'promoted' and its Steps become a dependency chain of
-- child jobs, each pointing back here so the chain stays traceable to the
-- job the user actually queued. Empty for an ordinary job.
ALTER TABLE jobs ADD COLUMN parent_job_id TEXT NOT NULL DEFAULT '';
