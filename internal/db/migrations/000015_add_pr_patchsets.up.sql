-- Every observed (head_sha, base_sha) pair for a PR over its
-- lifetime. A new row is inserted when sync sees a head SHA it
-- hasn't recorded before — that's equivalent to "the author
-- pushed a new patchset", whether the push was a fast-forward,
-- a force-push after a rebase, or a squash/amend.
--
-- Patchsets are the building block of Gerrit-style "compare
-- this push against that push" UX. Each patchset remembers
-- enough to reconstruct the PR state at that point in time.
CREATE TABLE IF NOT EXISTS middleman_pr_patchsets (
    id          INTEGER PRIMARY KEY,
    mr_id       INTEGER NOT NULL REFERENCES middleman_merge_requests(id) ON DELETE CASCADE,
    -- 1-based sequential number per MR, shown as "PS1", "PS2",
    -- etc. Assigned at insert time.
    number      INTEGER NOT NULL,
    head_sha    TEXT NOT NULL,
    -- base_sha captures what the PR was based on at that moment
    -- (the merge-base against the target branch, or the target
    -- branch tip if merge-base isn't available yet). Used to
    -- compute rebase-subtracted diffs.
    base_sha    TEXT NOT NULL DEFAULT '',
    -- merge_base_sha is the actual merge-base with the target
    -- branch at observation time. Optional — filled in when we
    -- can compute it from the clone.
    merge_base_sha TEXT NOT NULL DEFAULT '',
    observed_at DATETIME NOT NULL DEFAULT (datetime('now')),
    UNIQUE(mr_id, head_sha)
);

CREATE INDEX IF NOT EXISTS idx_pr_patchsets_mr_id
    ON middleman_pr_patchsets(mr_id, number);
