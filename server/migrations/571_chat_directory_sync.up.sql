CREATE TABLE chat_directory_sync (
 id uuid PRIMARY KEY,
 chat_session_id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 runtime_id uuid NOT NULL,
 resource_ref jsonb NOT NULL,
 status text NOT NULL DEFAULT 'pending',
 result jsonb,
 created_at timestamptz NOT NULL DEFAULT now()
);
