CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_agent_task_queue_chat_input_owner
ON agent_task_queue (chat_input_task_id) WHERE chat_input_task_id IS NOT NULL;
