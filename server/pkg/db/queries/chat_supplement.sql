-- name: CreateChatTaskSupplement :one
-- Session -> running task -> queued task matches chat terminal/cancellation
-- writers. The original input and queue priority remain untouched until ack.
WITH session AS MATERIALIZED (
    SELECT id FROM chat_session
    WHERE id = @chat_session_id AND workspace_id = @workspace_id
      AND creator_id = @author_id AND status = 'active'
    FOR UPDATE
), running AS MATERIALIZED (
    SELECT t.id, t.agent_id
    FROM agent_task_queue t
    JOIN session cs ON cs.id = t.chat_session_id
    JOIN task_supplement_capability cap ON cap.task_id = t.id
    WHERE t.id = @task_id AND t.status = 'running'
      AND t.issue_id IS NULL AND cap.capability = 'task-supplement-v1'
      AND COALESCE(t.context->>'chat_action', '') = ''
    FOR UPDATE OF t
), queued AS MATERIALIZED (
    SELECT q.id FROM agent_task_queue q
    JOIN running r ON r.agent_id = q.agent_id
    WHERE q.id = @queued_task_id AND q.chat_session_id = @chat_session_id
      AND q.status = 'queued' AND q.issue_id IS NULL
      AND COALESCE(q.context->>'chat_action', '') = ''
      AND COALESCE(q.chat_input_task_id, q.id) = q.id
      AND NOT EXISTS (
          SELECT 1 FROM chat_task_supplement s
          WHERE s.queued_task_id = q.id
            AND (s.task_id = @task_id OR s.status IN ('pending', 'delivering', 'delivered'))
      )
    FOR UPDATE OF q
)
INSERT INTO chat_task_supplement (
    task_id, queued_task_id, chat_message_id, chat_session_id, workspace_id,
    author_id, client_request_id, status
)
SELECT @task_id, q.id, m.id, @chat_session_id, @workspace_id,
       @author_id, @client_request_id, 'pending'
FROM queued q
JOIN chat_message m ON m.task_id = q.id AND m.role = 'user'
WHERE m.chat_session_id = @chat_session_id
  AND NOT m.channel_ingested
  AND (btrim(m.content) <> '' OR EXISTS (SELECT 1 FROM attachment a WHERE a.chat_message_id = m.id))
  AND (SELECT count(*) FROM chat_message other WHERE other.task_id = q.id AND other.role = 'user') = 1
RETURNING *;

-- name: GetChatTaskSupplementByRequest :one
SELECT * FROM chat_task_supplement
WHERE chat_session_id = @chat_session_id AND workspace_id = @workspace_id
  AND author_id = @author_id AND client_request_id = @client_request_id;

-- name: QueuedChatTaskHasActiveSupplement :one
SELECT EXISTS (
    SELECT 1 FROM chat_task_supplement
    WHERE queued_task_id = @queued_task_id AND workspace_id = @workspace_id
      AND status IN ('pending', 'delivering')
) AS active;

-- name: ListChatTaskSupplementsForSession :many
SELECT DISTINCT ON (queued_task_id) * FROM chat_task_supplement
WHERE chat_session_id = @chat_session_id AND workspace_id = @workspace_id
  AND queued_task_id = ANY(@queued_task_ids::uuid[])
ORDER BY queued_task_id, created_at DESC, chat_message_id;

-- name: ClaimNextChatTaskSupplement :one
WITH next AS MATERIALIZED (
    SELECT s.chat_message_id, s.task_id
    FROM chat_task_supplement s
    JOIN agent_task_queue t ON t.id = s.task_id
    JOIN agent_task_queue queued ON queued.id = s.queued_task_id
    JOIN task_supplement_capability cap ON cap.task_id = t.id
    WHERE s.task_id = @task_id AND s.status = 'pending'
      AND t.status = 'running' AND queued.status = 'queued'
      AND cap.capability = 'task-supplement-v1'
    ORDER BY s.created_at, s.chat_message_id
    FOR UPDATE OF s SKIP LOCKED LIMIT 1
), claimed AS (
    UPDATE chat_task_supplement s
    SET status = 'delivering', attempt_count = attempt_count + 1,
        failure_reason = NULL, updated_at = now()
    FROM next
    WHERE s.chat_message_id = next.chat_message_id AND s.task_id = next.task_id
    RETURNING s.*
)
SELECT claimed.chat_message_id, claimed.attempt_count, m.content,
       COALESCE(NULLIF(btrim(u.name), ''), 'a user')::text AS author_name
FROM claimed
JOIN chat_message m ON m.id = claimed.chat_message_id
LEFT JOIN "user" u ON u.id = claimed.author_id;

-- name: AckChatTaskSupplementDelivered :one
-- The session lock orders against terminal settlement, the agent lock against
-- the queue dispatcher. Late success may retire only input still in the queue.
WITH session AS MATERIALIZED (
    SELECT cs.id FROM chat_session cs
    JOIN agent_task_queue t ON t.chat_session_id = cs.id
    WHERE t.id = @task_id FOR UPDATE OF cs
), agent_lock AS MATERIALIZED (
    SELECT a.id FROM agent a
    JOIN agent_task_queue t ON t.agent_id = a.id
    JOIN session cs ON cs.id = t.chat_session_id
    WHERE t.id = @task_id FOR UPDATE OF a
), running AS MATERIALIZED (
    SELECT t.id FROM agent_task_queue t
    JOIN agent_lock a ON a.id = t.agent_id
    WHERE t.id = @task_id FOR UPDATE OF t
), queued AS MATERIALIZED (
    SELECT q.id FROM agent_task_queue q
    JOIN chat_task_supplement s ON s.queued_task_id = q.id
    JOIN running r ON r.id = s.task_id
    WHERE s.chat_message_id = @chat_message_id
      AND (q.status = 'queued' OR (q.status = 'cancelled' AND s.status = 'delivered'))
    FOR UPDATE OF q
), delivered AS (
    UPDATE chat_task_supplement s
    SET status = 'delivered', delivered_at = COALESCE(delivered_at, now()),
        failure_reason = NULL, updated_at = now()
    FROM queued q
    WHERE s.queued_task_id = q.id AND s.task_id = @task_id
      AND s.chat_message_id = @chat_message_id
      AND (s.status IN ('delivering', 'delivered')
           OR (s.status = 'failed' AND s.failure_reason = 'turn_ended' AND s.attempt_count > 0))
    RETURNING s.*
), moved AS (
    UPDATE chat_message m SET task_id = d.task_id
    FROM delivered d WHERE m.id = d.chat_message_id
    RETURNING m.id
), cancelled AS (
    UPDATE agent_task_queue q SET status = 'cancelled', completed_at = now()
    FROM delivered d WHERE q.id = d.queued_task_id AND q.status = 'queued'
    RETURNING q.id
)
SELECT delivered.* FROM delivered
WHERE (SELECT count(*) FROM moved) >= 0 AND (SELECT count(*) FROM cancelled) >= 0;

-- name: AckChatTaskSupplementFailed :one
UPDATE chat_task_supplement
SET status = 'failed', failure_reason = @failure_reason, updated_at = now()
WHERE task_id = @task_id AND chat_message_id = @chat_message_id
  AND status = 'delivering'
RETURNING *;

-- name: SettleTerminalChatTaskSupplements :execrows
UPDATE chat_task_supplement AS s
SET status = 'failed', failure_reason = 'turn_ended', updated_at = now()
WHERE s.status IN ('pending', 'delivering')
  AND (s.task_id IN (
      SELECT id FROM agent_task_queue
      WHERE id = ANY(@task_ids::uuid[]) AND status IN ('completed', 'failed', 'cancelled')
  ) OR s.queued_task_id IN (
      SELECT id FROM agent_task_queue
      WHERE id = ANY(@task_ids::uuid[]) AND status IN ('completed', 'failed', 'cancelled')
  ));

-- name: DeleteChatTaskSupplementsBySystemRuntimeAgents :exec
WITH sessions AS MATERIALIZED (
    SELECT cs.id FROM chat_session cs JOIN agent a ON a.id = cs.agent_id
    WHERE a.runtime_id = @runtime_id AND a.kind = 'system'
), receipts AS (
    DELETE FROM chat_task_supplement WHERE chat_session_id IN (SELECT id FROM sessions)
)
DELETE FROM task_supplement_capability
WHERE task_id IN (SELECT id FROM agent_task_queue WHERE chat_session_id IN (SELECT id FROM sessions));

-- name: ChatInputHasDeliveredSupplement :one
-- Delivered guidance makes this a multi-message conversation, even before the
-- provider emits output. Preserve the batch when cancelling any of its retries.
SELECT EXISTS (
    SELECT 1 FROM agent_task_queue receiving
    JOIN chat_task_supplement supplement ON supplement.task_id = receiving.id
    JOIN chat_session session ON session.id = supplement.chat_session_id
      AND session.workspace_id = supplement.workspace_id
    WHERE (receiving.id = @input_owner_id OR receiving.chat_input_task_id = @input_owner_id)
      AND receiving.chat_session_id = @chat_session_id
      AND supplement.chat_session_id = @chat_session_id
      AND supplement.status = 'delivered'
) AS delivered;
