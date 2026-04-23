-- Track which GitHub users (and teams) have been explicitly
-- asked to review a PR. Stored as a JSON array of login strings
-- (for teams: the team slug prefixed with "team:" so callers can
-- filter them out if they want). A NULL / empty array means the
-- sync hasn't pulled reviewers yet or the PR has none.
ALTER TABLE middleman_merge_requests
    ADD COLUMN requested_reviewers_json TEXT NOT NULL DEFAULT '[]';
