-- name: GetGlobalChatSessionByUser :one
SELECT * FROM global_chat_session
WHERE user_id = $1
LIMIT 1;

-- name: CreateGlobalChatSession :one
INSERT INTO global_chat_session (user_id, agent_id, title)
VALUES ($1, $2, $3)
RETURNING *;

-- name: ListGlobalChatMessages :many
-- Reverse-chronological page for the global session. The cursor is the
-- created_at of the last seen row; pass NULL for the first page. Limit is
-- clamped by the caller.
SELECT * FROM global_chat_message
WHERE global_session_id = $1
  AND (sqlc.narg('cursor_created')::timestamptz IS NULL
       OR created_at < sqlc.narg('cursor_created')::timestamptz)
ORDER BY created_at DESC
LIMIT $2;

-- name: InsertGlobalChatMessage :one
INSERT INTO global_chat_message
    (global_session_id, author_kind, author_id, body, metadata)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: AppendGlobalChatDispatchedTo :exec
-- Appends a dispatch record (a JSON object {workspace_id, mirror_session_id,
-- mirror_message_id}) onto the originating global_chat_message.dispatched_to
-- jsonb array. Used by GlobalDispatchService to record fan-out targets so
-- the global message remains an audit trail.
UPDATE global_chat_message
SET dispatched_to = dispatched_to || sqlc.arg('entry')::jsonb
WHERE id = sqlc.arg('id');
