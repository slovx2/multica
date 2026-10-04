CREATE UNIQUE INDEX CONCURRENTLY chat_supplement_request_uidx ON chat_task_supplement (chat_session_id, author_id, client_request_id);
