-- Worktrees discovered under a repo's configured local_path.
-- A row is inserted the first time `git worktree list --porcelain`
-- surfaces a (repo, path) pair; subsequent scans refresh the
-- branch/HEAD/state columns and bump last_seen_at.
--
-- Worktrees that disappear from the scan output get removed_at
-- set rather than being deleted, so that any data hung off the
-- row (future patchsets, comments, sessions) survives a temporary
-- absence — e.g. an unplugged drive or a worktree removed and
-- recreated at the same path.
CREATE TABLE IF NOT EXISTS middleman_worktrees (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    repo_id       INTEGER NOT NULL REFERENCES middleman_repos(id) ON DELETE CASCADE,
    path          TEXT NOT NULL,
    branch        TEXT NOT NULL DEFAULT '',
    head_sha      TEXT NOT NULL DEFAULT '',
    is_detached   INTEGER NOT NULL DEFAULT 0,
    is_locked     INTEGER NOT NULL DEFAULT 0,
    is_prunable   INTEGER NOT NULL DEFAULT 0,
    discovered_at DATETIME NOT NULL DEFAULT (datetime('now')),
    last_seen_at  DATETIME NOT NULL DEFAULT (datetime('now')),
    removed_at    DATETIME,
    UNIQUE(repo_id, path)
);

CREATE INDEX IF NOT EXISTS idx_worktrees_repo_active
    ON middleman_worktrees(repo_id) WHERE removed_at IS NULL;
