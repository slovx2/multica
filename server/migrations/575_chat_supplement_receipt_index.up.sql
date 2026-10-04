CREATE UNIQUE INDEX CONCURRENTLY chat_supplement_receipt_uidx ON chat_task_supplement (chat_message_id, task_id);
