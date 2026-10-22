CREATE TABLE IF NOT EXISTS middleman_ai_review_runs (
    id                INTEGER  PRIMARY KEY AUTOINCREMENT,
    merge_request_id  INTEGER  NOT NULL REFERENCES middleman_merge_requests(id) ON DELETE CASCADE,
    branch            TEXT     NOT NULL DEFAULT '',
    head_sha          TEXT     NOT NULL,
    read_sha          TEXT     NOT NULL,
    instructions      TEXT     NOT NULL DEFAULT '',
    claude_session_id TEXT     NOT NULL DEFAULT '',
    worktree_path     TEXT     NOT NULL DEFAULT '',
    status            TEXT     NOT NULL CHECK (status IN ('queued', 'running', 'done', 'failed', 'cancelled')),
    summary           TEXT     NOT NULL DEFAULT '',
    raw_json          TEXT     NOT NULL DEFAULT '',
    error             TEXT     NOT NULL DEFAULT '',
    pid               INTEGER,
    created_at        DATETIME NOT NULL,
    started_at        DATETIME,
    completed_at      DATETIME
);

CREATE INDEX IF NOT EXISTS idx_ai_review_runs_mr_branch
    ON middleman_ai_review_runs (merge_request_id, branch, id);

CREATE TABLE IF NOT EXISTS middleman_review_draft_messages (
    id           INTEGER  PRIMARY KEY AUTOINCREMENT,
    draft_id     TEXT     NOT NULL REFERENCES middleman_review_drafts(id) ON DELETE CASCADE,
    author       TEXT     NOT NULL CHECK (author IN ('user', 'claude')),
    kind         TEXT     NOT NULL CHECK (kind IN ('message', 'revision')),
    body         TEXT     NOT NULL DEFAULT '',
    prev_body    TEXT     NOT NULL DEFAULT '',
    applied      BOOLEAN  NOT NULL DEFAULT 0,
    status       TEXT     NOT NULL CHECK (status IN ('queued', 'running', 'done', 'failed', 'cancelled')),
    raw_json     TEXT     NOT NULL DEFAULT '',
    error        TEXT     NOT NULL DEFAULT '',
    pid          INTEGER,
    created_at   DATETIME NOT NULL,
    started_at   DATETIME,
    completed_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_review_draft_messages_draft
    ON middleman_review_draft_messages (draft_id, id);
