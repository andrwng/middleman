-- Per-PR record of review threads the reviewer has hidden from the UI.
-- One row per (merge_request, thread root) where the root is identified
-- by the GitHub comment id of the top-level review comment in that
-- thread. We use the GitHub platform id (not our autoincrement
-- middleman_mr_events.id) because re-syncs can replace local rows
-- but GitHub ids are stable.
--
-- "Currently hidden" is a derived predicate: a row is active iff
-- no review_comment in that thread has created_at > hidden_at. The
-- active set is computed at read time; stale rows are harmless and
-- a future re-hide UPSERTs a fresher hidden_at.
CREATE TABLE IF NOT EXISTS middleman_hidden_review_threads (
    merge_request_id         INTEGER  NOT NULL REFERENCES middleman_merge_requests(id) ON DELETE CASCADE,
    root_platform_comment_id INTEGER  NOT NULL,
    hidden_at                DATETIME NOT NULL,
    PRIMARY KEY (merge_request_id, root_platform_comment_id)
);
