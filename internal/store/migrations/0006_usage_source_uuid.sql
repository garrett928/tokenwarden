-- source_uuid lets historical-backfill entries (FR-USAGE-2, indexing
-- ~/.claude/projects/**/*.jsonl) be inserted idempotently: re-indexing the
-- same transcript line twice is a silent no-op rather than double-counting
-- usage. Empty for entries recorded from a live dispatch (internal/dispatch),
-- which have no natural external key.
ALTER TABLE usage_entries ADD COLUMN source_uuid TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX idx_usage_entries_source_uuid ON usage_entries(source_uuid) WHERE source_uuid != '';
