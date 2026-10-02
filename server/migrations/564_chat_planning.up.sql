ALTER TABLE chat_session ADD COLUMN IF NOT EXISTS plan_mode boolean NOT NULL DEFAULT false;

-- Soft relationships are validated and cleaned up by the application.
CREATE TABLE IF NOT EXISTS chat_card (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    workspace_id uuid NOT NULL,
    chat_session_id uuid NOT NULL,
    task_id uuid NOT NULL,
    source_key text NOT NULL,
    kind text NOT NULL CHECK (kind IN ('user_question', 'plan')),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'answered', 'approved', 'rejected', 'superseded')),
    payload jsonb NOT NULL,
    response jsonb,
    response_task_id uuid,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS issue_chat_session (
    workspace_id uuid NOT NULL,
    issue_id uuid NOT NULL,
    chat_session_id uuid NOT NULL,
    relation_type text NOT NULL DEFAULT 'planning' CHECK (relation_type = 'planning'),
    created_at timestamptz NOT NULL DEFAULT now()
);
