CREATE TABLE IF NOT EXISTS middleman_repo_diff_collapse_globs (
    repo_id  INTEGER NOT NULL REFERENCES middleman_repos(id) ON DELETE CASCADE,
    position INTEGER NOT NULL,
    glob     TEXT    NOT NULL,
    PRIMARY KEY (repo_id, position)
);
