DROP TABLE IF EXISTS issue_chat_session;
DROP TABLE IF EXISTS chat_card;
ALTER TABLE chat_session DROP COLUMN IF EXISTS plan_mode;
