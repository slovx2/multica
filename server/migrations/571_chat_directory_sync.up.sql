-- QORA-13 explicitly requires cascades for every parent deletion path.
CREATE TABLE chat_directory_sync (
 id uuid PRIMARY KEY,
 chat_session_id uuid NOT NULL REFERENCES chat_session(id) ON DELETE CASCADE,
 workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
 runtime_id uuid NOT NULL REFERENCES agent_runtime(id) ON DELETE CASCADE,
 resource_ref jsonb NOT NULL,
 status text NOT NULL DEFAULT 'pending',
 result jsonb,
 created_at timestamptz NOT NULL DEFAULT now()
);
