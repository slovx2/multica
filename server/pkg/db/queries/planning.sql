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
SELECT i.* FROM issue_chat_session l JOIN issue i ON i.id = l.issue_id AND i.workspace_id = l.workspace_id
WHERE l.workspace_id = $1 AND l.chat_session_id = $2 ORDER BY l.created_at DESC;

-- name: DeleteChatPlanningData :exec
WITH cards AS (DELETE FROM chat_card WHERE chat_card.chat_session_id = $1 AND chat_card.workspace_id = $2)
DELETE FROM issue_chat_session WHERE issue_chat_session.chat_session_id = $1 AND issue_chat_session.workspace_id = $2;

-- name: DeleteIssuePlanningLinks :exec
DELETE FROM issue_chat_session WHERE issue_id = $1 AND workspace_id = $2;
