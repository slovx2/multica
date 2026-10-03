CREATE INDEX CONCURRENTLY IF NOT EXISTS chat_directory_sync_pending_idx
ON chat_directory_sync (runtime_id, created_at) WHERE status = 'pending';
