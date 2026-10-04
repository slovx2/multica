-- Project requests may have no chat session. Discard this transient queue on
-- rollback; callers can retry with the restored session-based API.
DELETE FROM chat_directory_sync;

ALTER TABLE chat_directory_sync
    DROP COLUMN force,
    DROP COLUMN requester_id,
    DROP COLUMN project_id,
    ADD COLUMN chat_session_id uuid NOT NULL;
