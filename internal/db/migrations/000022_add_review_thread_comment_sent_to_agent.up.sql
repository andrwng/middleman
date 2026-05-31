-- Mark which review-thread comments were sent to the agent (an "Ask Claude"
-- reply), so the UI can flag them without changing the conversation flow.
ALTER TABLE middleman_review_thread_comments
    ADD COLUMN sent_to_agent BOOLEAN NOT NULL DEFAULT 0;
