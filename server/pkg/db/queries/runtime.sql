-- name: ListAgentRuntimes :many
SELECT * FROM agent_runtime
WHERE workspace_id = $1
ORDER BY created_at ASC;

-- name: GetAgentRuntime :one
SELECT * FROM agent_runtime
WHERE id = $1;

-- name: GetAgentRuntimeForWorkspace :one
SELECT * FROM agent_runtime
WHERE id = $1 AND workspace_id = $2;

-- name: UpsertAgentRuntime :one
INSERT INTO agent_runtime (
    workspace_id,
    daemon_id,
    name,
    runtime_mode,
    provider,
    status,
    device_info,
    metadata,
    owner_id,
    last_seen_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now())
ON CONFLICT (workspace_id, daemon_id, provider)
DO UPDATE SET
    name = EXCLUDED.name,
    runtime_mode = EXCLUDED.runtime_mode,
    status = EXCLUDED.status,
    device_info = EXCLUDED.device_info,
    metadata = EXCLUDED.metadata,
    owner_id = COALESCE(EXCLUDED.owner_id, agent_runtime.owner_id),
    last_seen_at = now(),
    updated_at = now()
RETURNING *;

-- name: UpdateAgentRuntimeHeartbeat :one
UPDATE agent_runtime
SET status = 'online', last_seen_at = now(), updated_at = now()
WHERE id = $1
RETURNING *;

-- name: SetAgentRuntimeOffline :exec
UPDATE agent_runtime
SET status = 'offline', updated_at = now()
WHERE id = $1;

-- name: MarkStaleRuntimesOffline :many
UPDATE agent_runtime
SET status = 'offline', updated_at = now()
WHERE status = 'online'
  AND last_seen_at < now() - make_interval(secs => @stale_seconds::double precision)
RETURNING id, workspace_id;

-- name: FailTasksForOfflineRuntimes :many
-- Marks dispatched/running tasks as failed when their runtime is offline.
-- This cleans up orphaned tasks after a daemon crash or network partition.
UPDATE agent_task_queue
SET status = 'failed', completed_at = now(), error = 'runtime went offline'
WHERE status IN ('dispatched', 'running')
  AND runtime_id IN (
    SELECT id FROM agent_runtime WHERE status = 'offline'
  )
RETURNING id, agent_id, issue_id;

-- name: ListAgentRuntimesByOwner :many
SELECT * FROM agent_runtime
WHERE workspace_id = $1 AND owner_id = $2
ORDER BY created_at ASC;

-- name: DeleteAgentRuntime :exec
DELETE FROM agent_runtime WHERE id = $1;

-- name: CountActiveAgentsByRuntime :one
SELECT count(*) FROM agent WHERE runtime_id = $1 AND archived_at IS NULL;

-- name: DeleteArchivedAgentsByRuntime :exec
DELETE FROM agent WHERE runtime_id = $1 AND archived_at IS NOT NULL;

-- name: MigrateAgentsFromLegacyDaemon :execrows
-- Reparents agents from the legacy (hostname-derived) runtime row to the
-- newly registered UUID row. Scoped to a single (workspace, provider,
-- owner) triple so we never touch another user's runtimes even if they
-- share a hostname on the same machine. Called once per legacy daemon_id
-- candidate reported by the daemon at registration time.
UPDATE agent
SET runtime_id = @new_runtime_id
WHERE runtime_id IN (
    SELECT ar.id FROM agent_runtime ar
    WHERE ar.workspace_id = @workspace_id
      AND ar.provider = @provider
      AND ar.owner_id = @owner_id
      AND ar.id != @new_runtime_id
      AND ar.daemon_id = @legacy_daemon_id
);

-- name: MigrateTasksFromLegacyDaemon :execrows
-- Same scoping as MigrateAgentsFromLegacyDaemon. Must run before the DELETE
-- below because agent_task_queue.runtime_id is ON DELETE CASCADE; deleting
-- the legacy row first would silently drop in-flight tasks.
UPDATE agent_task_queue
SET runtime_id = @new_runtime_id
WHERE runtime_id IN (
    SELECT ar.id FROM agent_runtime ar
    WHERE ar.workspace_id = @workspace_id
      AND ar.provider = @provider
      AND ar.owner_id = @owner_id
      AND ar.id != @new_runtime_id
      AND ar.daemon_id = @legacy_daemon_id
);

-- name: DeleteLegacyRuntime :execrows
-- Removes the stale hostname-derived runtime row once its agents and tasks
-- have been reparented. legacy_daemon_id on the new row captures the last
-- removed value as a breadcrumb.
DELETE FROM agent_runtime
WHERE workspace_id = @workspace_id
  AND provider = @provider
  AND owner_id = @owner_id
  AND id != @new_runtime_id
  AND daemon_id = @legacy_daemon_id;

-- name: SetRuntimeLegacyDaemonID :exec
UPDATE agent_runtime
SET legacy_daemon_id = @legacy_daemon_id
WHERE id = @id;

-- name: DeleteStaleOfflineRuntimes :many
-- Deletes runtimes that have been offline for longer than the TTL and have
-- no agents bound (active or archived). The FK constraint on agent.runtime_id
-- is ON DELETE RESTRICT, so we must exclude all agent references.
DELETE FROM agent_runtime
WHERE status = 'offline'
  AND last_seen_at < now() - make_interval(secs => @stale_seconds::double precision)
  AND id NOT IN (SELECT DISTINCT runtime_id FROM agent)
RETURNING id, workspace_id;
