CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS issue_chat_session_key_idx ON issue_chat_session (workspace_id, issue_id, chat_session_id);
