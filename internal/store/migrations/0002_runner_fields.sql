-- Fields the runner (Phase 3) needs beyond what 0001_init gave the fixed
-- job kinds: freeform's "user-specified" permission mode and filesystem
-- access, and research's optional --json-schema. See internal/store/job.go
-- and internal/runner/safety.go for how these are consumed.
ALTER TABLE jobs ADD COLUMN permission_mode    TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN allowed_tools      TEXT NOT NULL DEFAULT '[]'; -- JSON []string
ALTER TABLE jobs ADD COLUMN add_dirs           TEXT NOT NULL DEFAULT '[]'; -- JSON []string
ALTER TABLE jobs ADD COLUMN json_schema        TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN freeform_worktree  INTEGER NOT NULL DEFAULT 0;
