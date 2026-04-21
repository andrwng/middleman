CREATE TABLE IF NOT EXISTS middleman_ai_threads (
    id                  INTEGER PRIMARY KEY,
    mr_id               INTEGER NOT NULL REFERENCES middleman_merge_requests(id) ON DELETE CASCADE,
    path                TEXT NOT NULL,
    anchor_side         TEXT NOT NULL,   -- 'LEFT' or 'RIGHT'
    anchor_line         INTEGER NOT NULL,
    hunk_start_line     INTEGER,
    hunk_end_line       INTEGER,
    selection_text      TEXT,
    commit_sha          TEXT NOT NULL,
    claude_session_id   TEXT,
    worktree_path       TEXT,
    status              TEXT NOT NULL DEFAULT 'active', -- 'active' | 'closed'
    created_at          DATETIME NOT NULL DEFAULT (datetime('now')),
    closed_at           DATETIME
);

CREATE INDEX IF NOT EXISTS idx_ai_threads_mr_id
    ON middleman_ai_threads(mr_id);

CREATE INDEX IF NOT EXISTS idx_ai_threads_mr_status
    ON middleman_ai_threads(mr_id, status);

CREATE TABLE IF NOT EXISTS middleman_ai_questions (
    id             INTEGER PRIMARY KEY,
    thread_id      INTEGER NOT NULL REFERENCES middleman_ai_threads(id) ON DELETE CASCADE,
    question       TEXT NOT NULL,
    answer         TEXT NOT NULL DEFAULT '',
    citations_json TEXT NOT NULL DEFAULT '[]',
    error          TEXT NOT NULL DEFAULT '',
    status         TEXT NOT NULL DEFAULT 'queued', -- 'queued' | 'running' | 'done' | 'cancelled' | 'failed'
    pid            INTEGER,
    created_at     DATETIME NOT NULL DEFAULT (datetime('now')),
    started_at     DATETIME,
    completed_at   DATETIME
);

CREATE INDEX IF NOT EXISTS idx_ai_questions_thread_id
    ON middleman_ai_questions(thread_id);

CREATE INDEX IF NOT EXISTS idx_ai_questions_status
    ON middleman_ai_questions(status);
