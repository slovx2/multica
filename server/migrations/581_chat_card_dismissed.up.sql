ALTER TABLE chat_card DROP CONSTRAINT IF EXISTS chat_card_status_check;
ALTER TABLE chat_card ADD CONSTRAINT chat_card_status_check
    CHECK (status IN ('pending', 'answered', 'approved', 'rejected', 'superseded', 'dismissed'));
