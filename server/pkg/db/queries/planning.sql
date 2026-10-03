-- name: UpdateChatPlanMode :one
UPDATE chat_session SET plan_mode = $3, updated_at = now()
WHERE id = $1 AND workspace_id = $2 RETURNING *;

-- name: InsertChatCard :one
INSERT INTO chat_card (workspace_id, chat_session_id, task_id, source_key, kind, payload)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (chat_session_id, source_key) DO UPDATE SET source_key = EXCLUDED.source_key
RETURNING *;

-- name: ListChatCards :many
SELECT * FROM chat_card WHERE workspace_id = $1 AND chat_session_id = $2 ORDER BY created_at, id;

-- name: GetChatCardForUpdate :one
SELECT * FROM chat_card WHERE id = $1 AND workspace_id = $2 AND chat_session_id = $3 FOR UPDATE;

-- name: ResolveChatCard :one
UPDATE chat_card SET status = $4, response = $5, response_task_id = $6, updated_at = now()
WHERE id = $1 AND workspace_id = $2 AND chat_session_id = $3 AND status = 'pending' RETURNING *;

-- name: SupersedeChatPlans :exec
UPDATE chat_card SET status = 'superseded', updated_at = now()
WHERE workspace_id = $1 AND chat_session_id = $2 AND kind = 'plan' AND status = 'pending' AND id != $3;

-- name: LinkPlanningChat :exec
INSERT INTO issue_chat_session (workspace_id, issue_id, chat_session_id) VALUES ($1, $2, $3)
ON CONFLICT (workspace_id, issue_id, chat_session_id) DO NOTHING;

-- name: UnlinkPlanningChat :exec
DELETE FROM issue_chat_session WHERE workspace_id = $1 AND issue_id = $2 AND chat_session_id = $3;

-- name: ListIssuePlanningChats :many
SELECT cs.* FROM issue_chat_session l JOIN chat_session cs ON cs.id = l.chat_session_id AND cs.workspace_id = l.workspace_id
WHERE l.workspace_id = $1 AND l.issue_id = $2 ORDER BY l.created_at DESC;

-- name: ListChatPlanningIssues :many
-- Creation provenance predates planning links. Include those historical issues
-- in the created-issues view without inserting routing associations: unlinking
-- a planning chat must still stop mention routing, but cannot erase creation.
WITH related AS (
    SELECT l.issue_id, l.created_at FROM issue_chat_session l
    WHERE l.workspace_id = $1 AND l.chat_session_id = $2
    UNION ALL
    SELECT i.id, i.created_at FROM issue i
    JOIN agent_task_queue t ON i.origin_type = 'agent_create' AND i.origin_id = t.id
    JOIN chat_session cs ON cs.id = t.chat_session_id AND cs.workspace_id = i.workspace_id
    WHERE i.workspace_id = $1 AND cs.id = $2
      AND i.creator_type = 'agent' AND i.creator_id = t.agent_id AND cs.agent_id = t.agent_id
), deduplicated AS (
    SELECT issue_id, max(created_at) AS linked_at FROM related GROUP BY issue_id
)
SELECT i.* FROM deduplicated l JOIN issue i ON i.id = l.issue_id
WHERE i.workspace_id = $1 ORDER BY l.linked_at DESC, i.id DESC;

-- name: DeleteChatPlanningData :exec
WITH cards AS (DELETE FROM chat_card WHERE chat_card.chat_session_id = $1 AND chat_card.workspace_id = $2)
DELETE FROM issue_chat_session WHERE issue_chat_session.chat_session_id = $1 AND issue_chat_session.workspace_id = $2;

-- name: DeleteIssuePlanningLinks :exec
DELETE FROM issue_chat_session WHERE issue_id = $1 AND workspace_id = $2;
