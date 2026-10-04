-- A dismissed question has been resolved; retain that fact on rollback.
UPDATE chat_card SET status = 'answered' WHERE status = 'dismissed';
ALTER TABLE chat_card DROP CONSTRAINT IF EXISTS chat_card_status_check;
ALTER TABLE chat_card ADD CONSTRAINT chat_card_status_check
    CHECK (status IN ('pending', 'answered', 'approved', 'rejected', 'superseded'));
