ALTER TABLE chat_session ADD COLUMN IF NOT EXISTS execution_overrides jsonb NOT NULL DEFAULT '{}';
