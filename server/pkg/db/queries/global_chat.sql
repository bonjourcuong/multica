-- name: GetGlobalChatSessionByUser :one
SELECT * FROM global_chat_session
WHERE user_id = $1
LIMIT 1;

-- name: GetGlobalChatSession :one
-- Lookup by primary key. Backs the daemon claim-task path and the task
-- callback writeback: TaskService.CompleteTask resolves the owning user
-- from this row before publishing the per-user realtime event.
SELECT * FROM global_chat_session
WHERE id = $1;

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

-- name: ListGlobalMirrorsByUser :many
-- One row per workspace the user belongs to. Mirror session columns are NULL
-- for workspaces the user has never dispatched to (the mirror session is
-- created lazily on first @workspace mention). Membership is enforced inside
-- the JOIN so the endpoint can sit outside the per-workspace middleware (ADR
-- R5): a user passing through the route only ever sees mirrors for
-- (m.user_id = $1) workspaces.
--
-- unread_count counts assistant-authored mirror messages whose created_at is
-- at or after chat_session.unread_since — which is the timestamp of the
-- first uncleared assistant reply (see migration 040). 0 when the session
-- has no unread, NULL-safe via the LEFT JOIN.
--
-- Ordered by recent activity so the tile grid lands with the most active
-- workspaces on the left. NULLS LAST keeps cold workspaces at the tail.
SELECT
    w.id            AS workspace_id,
    w.slug          AS workspace_slug,
    w.name          AS workspace_name,
    cs.id           AS mirror_session_id,
    stats.last_message_at::timestamptz AS last_message_at,
    COALESCE(stats.unread_count, 0)::int AS unread_count
FROM member m
JOIN workspace w ON w.id = m.workspace_id
LEFT JOIN chat_session cs
    ON cs.workspace_id = w.id
   AND cs.scope        = 'global_mirror'
   AND cs.creator_id   = m.user_id
LEFT JOIN LATERAL (
    SELECT
        MAX(cm.created_at) AS last_message_at,
        COUNT(*) FILTER (
            WHERE cs.unread_since IS NOT NULL
              AND cm.role        = 'assistant'
              AND cm.created_at >= cs.unread_since
        ) AS unread_count
    FROM chat_message cm
    WHERE cm.chat_session_id = cs.id
) stats ON TRUE
WHERE m.user_id = $1
ORDER BY stats.last_message_at DESC NULLS LAST, w.name ASC
LIMIT $2;
