ALTER TABLE chat_session ADD COLUMN context_state jsonb NOT NULL DEFAULT '{}';
