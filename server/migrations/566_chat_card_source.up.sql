CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS chat_card_source_idx ON chat_card (chat_session_id, source_key);
