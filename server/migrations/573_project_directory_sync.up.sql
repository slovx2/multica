ALTER TABLE chat_directory_sync
    ADD COLUMN project_id uuid,
    ADD COLUMN requester_id uuid,
    ADD COLUMN force boolean NOT NULL DEFAULT false;

UPDATE chat_directory_sync AS sync
SET project_id = session.project_id, requester_id = session.creator_id
FROM chat_session AS session
WHERE sync.chat_session_id = session.id;

-- Sync requests are transient; sessions without a project cannot be migrated.
DELETE FROM chat_directory_sync WHERE project_id IS NULL OR requester_id IS NULL;

ALTER TABLE chat_directory_sync
    ALTER COLUMN project_id SET NOT NULL,
    ALTER COLUMN requester_id SET NOT NULL,
    DROP COLUMN chat_session_id;
