-- Local-worktree review threads. A "review" is the living set of these
-- threads on a worktree's synthetic merge request; each thread anchors to
-- a file/line in the worktree diff and owns a comment conversation
-- (reviewer +, later, agent). Distinct from middleman_ai_threads (Q&A,
-- one Claude session per thread) and from GitHub-synced review comments in
-- middleman_mr_events (remote-only).
CREATE TABLE IF NOT EXISTS middleman_review_threads (
    id          INTEGER PRIMARY KEY,
    mr_id       INTEGER  NOT NULL REFERENCES middleman_merge_requests(id) ON DELETE CASCADE,
    path        TEXT     NOT NULL,
    side        TEXT     NOT NULL,                 -- 'LEFT' | 'RIGHT'
    line        INTEGER  NOT NULL,
    start_line  INTEGER,                           -- nullable; multi-line selection start
    commit_sha  TEXT     NOT NULL,
    status      TEXT     NOT NULL DEFAULT 'open',  -- 'open' | 'discussed' | 'applied' | 'resolved'
    hidden_at   DATETIME,                          -- non-null => hidden from the UI
    created_at  DATETIME NOT NULL DEFAULT (datetime('now')),
    updated_at  DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_review_threads_mr_id
    ON middleman_review_threads(mr_id);

CREATE INDEX IF NOT EXISTS idx_review_threads_mr_status
    ON middleman_review_threads(mr_id, status);

-- Comments within a review thread: the reviewer's root comment, agent
-- replies (Phase 2+), and reviewer follow-ups. Oldest-first by id.
CREATE TABLE IF NOT EXISTS middleman_review_thread_comments (
    id         INTEGER  PRIMARY KEY,
    thread_id  INTEGER  NOT NULL REFERENCES middleman_review_threads(id) ON DELETE CASCADE,
    author     TEXT     NOT NULL,                  -- 'user' | 'agent'
    body       TEXT     NOT NULL,
    turn_id    INTEGER,                            -- nullable; worktree_session_turns.id for agent replies (Phase 2)
    created_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_review_thread_comments_thread_id
    ON middleman_review_thread_comments(thread_id);
