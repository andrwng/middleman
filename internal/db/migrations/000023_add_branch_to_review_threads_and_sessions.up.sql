-- Branch-scope a worktree's review threads and its in-app --resume
-- session. Phase 3 makes a "review" tied to its head branch (like a
-- GitHub PR), so switching branches in one worktree no longer shares a
-- thread-set or an agent conversation.
ALTER TABLE middleman_review_threads
    ADD COLUMN branch TEXT NOT NULL DEFAULT '';

ALTER TABLE middleman_worktree_sessions
    ADD COLUMN branch TEXT NOT NULL DEFAULT '';

-- Backfill existing review-thread rows from their worktree's scanned
-- branch. The synthetic MR's platform_id is the worktree id
-- (see internal/server/local_dispatch.go), so:
--   review_thread.mr_id -> merge_request.platform_id -> worktree.branch
-- Rows that resolve to nothing keep ''.
UPDATE middleman_review_threads
   SET branch = (
       SELECT w.branch
         FROM middleman_merge_requests mr
         JOIN middleman_worktrees w ON w.id = mr.platform_id
        WHERE mr.id = middleman_review_threads.mr_id
          AND mr.repo_id = w.repo_id
   )
 WHERE EXISTS (
       SELECT 1
         FROM middleman_merge_requests mr
         JOIN middleman_worktrees w ON w.id = mr.platform_id
        WHERE mr.id = middleman_review_threads.mr_id
          AND mr.repo_id = w.repo_id
 );

-- Backfill active session rows from their worktree's scanned branch.
UPDATE middleman_worktree_sessions
   SET branch = (
       SELECT w.branch
         FROM middleman_worktrees w
        WHERE w.id = middleman_worktree_sessions.worktree_id
   )
 WHERE EXISTS (
       SELECT 1
         FROM middleman_worktrees w
        WHERE w.id = middleman_worktree_sessions.worktree_id
 );
