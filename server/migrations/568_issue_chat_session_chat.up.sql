CREATE INDEX CONCURRENTLY IF NOT EXISTS issue_chat_session_chat_idx ON issue_chat_session (workspace_id, chat_session_id, created_at);
