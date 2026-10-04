-- name: UpdateChatContextState :exec
UPDATE chat_session SET context_state = context_state || sqlc.arg(state)::jsonb
WHERE id = sqlc.arg(id) AND workspace_id = sqlc.arg(workspace_id);

-- name: SetChatTaskAction :one
UPDATE agent_task_queue SET context = COALESCE(context, '{}'::jsonb) || jsonb_build_object('chat_action', sqlc.arg(action)::text)
WHERE id = sqlc.arg(id) AND chat_session_id IS NOT NULL RETURNING *;

-- name: CreateChatDirectorySync :one
INSERT INTO chat_directory_sync (id, project_id, workspace_id, runtime_id, resource_ref, requester_id, force)
VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id;

-- name: ClaimChatDirectorySync :many
UPDATE chat_directory_sync SET status = 'running'
WHERE id IN (SELECT id FROM chat_directory_sync WHERE chat_directory_sync.runtime_id = $1 AND chat_directory_sync.status = 'pending' AND chat_directory_sync.created_at > now() - interval '1 minute' ORDER BY chat_directory_sync.created_at LIMIT 4 FOR UPDATE SKIP LOCKED)
RETURNING id, resource_ref, force;

-- name: CompleteChatDirectorySync :one
UPDATE chat_directory_sync SET status = 'completed', result = sqlc.arg(result)
WHERE id = sqlc.arg(id) AND runtime_id = sqlc.arg(runtime_id) AND status = 'running'
RETURNING project_id, workspace_id;

-- name: GetChatDirectorySync :one
SELECT id, status, result, created_at FROM chat_directory_sync
WHERE id = $1 AND project_id = $2 AND workspace_id = $3 AND requester_id = $4;

-- name: DeleteExpiredChatDirectorySync :exec
DELETE FROM chat_directory_sync WHERE created_at < now() - interval '1 day';

-- name: DeleteProjectDirectorySync :exec
DELETE FROM chat_directory_sync WHERE project_id = $1 AND workspace_id = $2;
