ALTER TABLE middleman_ai_review_runs ADD COLUMN base_sha TEXT NOT NULL DEFAULT '';
ALTER TABLE middleman_ai_review_runs ADD COLUMN local_worktree_path TEXT NOT NULL DEFAULT '';
