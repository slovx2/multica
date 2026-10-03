-- name: UpdateChatExecutionOverrides :one
UPDATE chat_session SET execution_overrides = $3, updated_at = now()
WHERE id = $1 AND workspace_id = $2 RETURNING *;
