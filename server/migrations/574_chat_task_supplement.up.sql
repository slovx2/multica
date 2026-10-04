-- Relationships are application-owned; no foreign keys or trigger cascades.
ALTER TABLE task_supplement_capability ALTER COLUMN issue_id DROP NOT NULL;
CREATE TABLE chat_task_supplement (
    task_id UUID NOT NULL,
    queued_task_id UUID NOT NULL,
    chat_message_id UUID NOT NULL,
    chat_session_id UUID NOT NULL,
    workspace_id UUID NOT NULL,
    author_id UUID NOT NULL,
    client_request_id UUID NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending', 'delivering', 'delivered', 'failed')),
    failure_reason TEXT,
    attempt_count INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    delivered_at TIMESTAMPTZ
);
