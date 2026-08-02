CREATE TABLE jobs (
    id              TEXT PRIMARY KEY,
    kind            TEXT NOT NULL,
    prompt          TEXT NOT NULL,
    workspace       TEXT NOT NULL DEFAULT '',
    model           TEXT NOT NULL DEFAULT '',
    effort          TEXT NOT NULL DEFAULT '',
    attachments     TEXT NOT NULL DEFAULT '[]',  -- JSON []Attachment
    steps           TEXT NOT NULL DEFAULT '[]',  -- JSON []string
    resumable       INTEGER NOT NULL DEFAULT 0,
    priority        INTEGER NOT NULL DEFAULT 0,
    earliest_at     INTEGER,                     -- unix seconds, nullable
    deadline_at     INTEGER,                     -- unix seconds, nullable
    max_budget_usd  REAL,                        -- nullable
    depends_on      TEXT NOT NULL DEFAULT '[]',  -- JSON []string of job ids
    session_id      TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'queued',
    failure_reason  TEXT NOT NULL DEFAULT '',
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL
);

CREATE INDEX idx_jobs_status ON jobs(status);
CREATE INDEX idx_jobs_priority ON jobs(priority DESC, created_at ASC);
