CREATE TABLE IF NOT EXISTS middleman_code_browser_state (
    mr_id      INTEGER PRIMARY KEY REFERENCES middleman_merge_requests(id) ON DELETE CASCADE,
    path       TEXT NOT NULL DEFAULT '',
    updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
);
