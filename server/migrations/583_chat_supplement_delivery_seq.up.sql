ALTER TABLE chat_task_supplement
    ADD COLUMN delivered_after_seq integer CHECK (delivered_after_seq >= 0);
