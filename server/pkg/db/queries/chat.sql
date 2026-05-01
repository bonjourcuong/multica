-- name: CreateChatSession :one
INSERT INTO chat_session (workspace_id, agent_id, creator_id, title)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetChatSession :one
SELECT * FROM chat_session
WHERE id = $1;

-- name: GetChatSessionInWorkspace :one
SELECT * FROM chat_session
WHERE id = $1 AND workspace_id = $2;

-- name: ListChatSessionsByCreator :many
-- Returns active sessions with a boolean unread flag. Unread is strictly
-- per-session: either the user has uncleared assistant replies in this
-- session or they don't. Counting messages would be misleading.
SELECT cs.*,
       (cs.unread_since IS NOT NULL)::bool AS has_unread
FROM chat_session cs
WHERE cs.workspace_id = $1 AND cs.creator_id = $2 AND cs.status = 'active'
ORDER BY cs.updated_at DESC;

-- name: ListAllChatSessionsByCreator :many
SELECT cs.*,
       (cs.unread_since IS NOT NULL)::bool AS has_unread
FROM chat_session cs
WHERE cs.workspace_id = $1 AND cs.creator_id = $2
ORDER BY cs.updated_at DESC;

-- name: UpdateChatSessionTitle :one
UPDATE chat_session SET title = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateChatSessionSession :exec
-- Updates the resume pointer for a chat session. Empty/NULL inputs are
-- ignored via COALESCE so a task that completes without a session_id (e.g.
-- the agent crashed before establishing one) cannot wipe out a previously
-- recorded resume pointer. This makes the chat memory robust against
-- intermittent agent failures.
UPDATE chat_session
SET session_id = COALESCE(sqlc.narg('session_id'), session_id),
    work_dir = COALESCE(sqlc.narg('work_dir'), work_dir),
    updated_at = now()
WHERE id = sqlc.arg('id');

-- name: ArchiveChatSession :exec
UPDATE chat_session SET status = 'archived', updated_at = now()
WHERE id = $1;

-- name: GetActiveChatSessionByCreatorAndAgent :one
-- Looks up the most recently touched workspace-scope active session for the
-- (workspace, creator, agent) triple. Backs the find-or-create endpoint that
-- the global-chat V2 lanes use so reopening a lane lands on the existing
-- thread instead of forking a new one.
--
-- The `scope = 'workspace'` predicate is essential: must NOT match
-- 'global_mirror' rows (the per-workspace twin sessions used by the global
-- broadcaster). Removing it would silently route lane traffic into the
-- mirror thread.
SELECT * FROM chat_session
WHERE workspace_id = $1
  AND creator_id   = $2
  AND agent_id     = $3
  AND status       = 'active'
  AND scope        = 'workspace'
ORDER BY updated_at DESC
LIMIT 1;

-- name: TouchChatSession :exec
UPDATE chat_session SET updated_at = now()
WHERE id = $1;

-- name: CreateChatMessage :one
INSERT INTO chat_message (chat_session_id, role, content, task_id)
VALUES ($1, $2, $3, sqlc.narg(task_id))
RETURNING *;

-- name: ListChatMessages :many
SELECT * FROM chat_message
WHERE chat_session_id = $1
ORDER BY created_at ASC;

-- name: GetChatMessage :one
SELECT * FROM chat_message
WHERE id = $1;

-- name: CreateChatTask :one
INSERT INTO agent_task_queue (agent_id, runtime_id, issue_id, status, priority, chat_session_id)
VALUES ($1, $2, NULL, 'queued', $3, $4)
RETURNING *;

-- name: CreateGlobalChatTask :one
-- Mirrors CreateChatTask but targets a global_chat_session instead of
-- a workspace chat_session. Both columns are mutually exclusive on a
-- single row by service-layer convention (a task either runs in a
-- workspace chat or in the global lane, never both); ClaimAgentTask
-- already ignores global_session_id, which is fine — the per-(agent,
-- chat_session) serialization rule still holds via the agent itself.
INSERT INTO agent_task_queue (agent_id, runtime_id, issue_id, status, priority, global_session_id)
VALUES ($1, $2, NULL, 'queued', $3, $4)
RETURNING *;

-- name: GetLastChatTaskSession :one
-- Returns the most recent task in this chat session that managed to record a
-- session_id. Includes both completed and failed tasks: even a failed task
-- may have established a real agent session before failing, and we'd rather
-- resume there than start over and lose conversation memory. Used as a
-- fallback when chat_session.session_id is NULL.
SELECT session_id, work_dir FROM agent_task_queue
WHERE chat_session_id = $1
  AND status IN ('completed', 'failed')
  AND session_id IS NOT NULL
ORDER BY completed_at DESC
LIMIT 1;

-- name: GetPendingChatTask :one
-- Returns the most recent in-flight task for a chat session, if any.
-- Used by the frontend to recover pending state after refresh / reopen.
SELECT id, status FROM agent_task_queue
WHERE chat_session_id = $1 AND status IN ('queued', 'dispatched', 'running')
ORDER BY created_at DESC
LIMIT 1;

-- name: ListPendingChatTasksByCreator :many
-- Aggregate view of all in-flight chat tasks owned by a given creator in a
-- workspace. Drives the FAB's "running" indicator when the chat window is
-- closed and no single session's query is active.
SELECT atq.id AS task_id, atq.status, atq.chat_session_id
FROM agent_task_queue atq
JOIN chat_session cs ON cs.id = atq.chat_session_id
WHERE cs.workspace_id = $1
  AND cs.creator_id = $2
  AND atq.status IN ('queued', 'dispatched', 'running')
ORDER BY atq.created_at DESC;

-- name: MarkChatSessionRead :exec
-- Clears unread_since, dropping the session's unread count to 0.
UPDATE chat_session SET unread_since = NULL
WHERE id = $1;

-- name: SetUnreadSinceIfNull :exec
-- Atomically stamps the first unread assistant message's arrival time.
-- No-op if the session is already in "has unread" state — keeps the earliest
-- unread boundary stable across multiple incoming replies.
UPDATE chat_session SET unread_since = now()
WHERE id = $1 AND unread_since IS NULL;

-- name: GetGlobalMirrorSession :one
-- Looks up the per-workspace "Cuong Global" mirror session for a given user.
-- Identity is (workspace_id, scope='global_mirror', creator_id), NOT the
-- title — the user-facing name is cosmetic.
SELECT * FROM chat_session
WHERE workspace_id = $1
  AND scope = 'global_mirror'
  AND creator_id = $2
LIMIT 1;

-- name: CreateGlobalMirrorSession :one
-- Creates the workspace-side "Cuong Global" mirror session. agent_id points
-- at the workspace-resident global mirror agent (pre-existing in the
-- workspace) — for V1 we reuse the user's workspace-default agent, which
-- the dispatch service resolves before calling this. The cosmetic title is
-- "Cuong Global"; lookup is by (workspace_id, scope, creator_id).
INSERT INTO chat_session (workspace_id, agent_id, creator_id, title, scope)
VALUES ($1, $2, $3, 'Cuong Global', 'global_mirror')
RETURNING *;

-- name: InsertMirrorChatMessage :one
-- Appends a message to the workspace-side mirror session. metadata carries
-- the {global_origin: {...}} pointer back to the originating global message.
INSERT INTO chat_message (chat_session_id, role, content, metadata)
VALUES ($1, $2, $3, $4)
RETURNING *;
