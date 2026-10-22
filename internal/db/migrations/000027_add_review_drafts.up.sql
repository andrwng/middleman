CREATE TABLE IF NOT EXISTS middleman_review_drafts (
    id                 TEXT     PRIMARY KEY,
    merge_request_id   INTEGER  NOT NULL REFERENCES middleman_merge_requests(id) ON DELETE CASCADE,
    branch             TEXT     NOT NULL DEFAULT '',
    path               TEXT     NOT NULL,
    side               TEXT     NOT NULL CHECK (side IN ('LEFT', 'RIGHT')),
    line               INTEGER  NOT NULL,
    start_line         INTEGER,
    commit_sha         TEXT     NOT NULL,
    in_reply_to        INTEGER,
    body               TEXT     NOT NULL,
    origin             TEXT     NOT NULL CHECK (origin IN ('user', 'claude')),
    state              TEXT     NOT NULL CHECK (state IN ('suggestion', 'draft', 'discarded')),
    discarded_from     TEXT     NOT NULL DEFAULT '',
    run_id             INTEGER,
    claude_session_id  TEXT     NOT NULL DEFAULT '',
    chat_worktree_path TEXT     NOT NULL DEFAULT '',
    submit_error       TEXT     NOT NULL DEFAULT '',
    created_at         DATETIME NOT NULL,
    updated_at         DATETIME NOT NULL,
    body_edited_at     DATETIME
);

CREATE INDEX IF NOT EXISTS idx_review_drafts_mr_branch_state
    ON middleman_review_drafts (merge_request_id, branch, state);

CREATE TABLE IF NOT EXISTS middleman_review_draft_meta (
    merge_request_id INTEGER  NOT NULL REFERENCES middleman_merge_requests(id) ON DELETE CASCADE,
    branch           TEXT     NOT NULL DEFAULT '',
    body             TEXT     NOT NULL DEFAULT '',
    event            TEXT     NOT NULL DEFAULT '',
    updated_at       DATETIME NOT NULL,
    PRIMARY KEY (merge_request_id, branch)
);
