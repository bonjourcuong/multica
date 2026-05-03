-- name: CreateDaemonToken :one
INSERT INTO daemon_token (token_hash, workspace_id, daemon_id, user_id, expires_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetDaemonTokenByHash :one
SELECT * FROM daemon_token
WHERE token_hash = $1 AND expires_at > now();

-- name: DeleteDaemonTokensByWorkspaceAndDaemon :exec
DELETE FROM daemon_token
WHERE workspace_id = $1 AND daemon_id = $2;

-- name: DeleteExpiredDaemonTokens :exec
DELETE FROM daemon_token
WHERE expires_at <= now();

-- name: ListDaemonTokensByWorkspaceAndDaemon :many
-- Used by the mint handler to reject cross-user same-daemon_id mints
-- (MUL-201, ADR 2026-05-03 D9 step 3). Standard SQL `=` semantics; NULL
-- daemon_id rows would be excluded automatically (the column is NOT NULL,
-- so this is moot today, but the comment pins the contract).
SELECT * FROM daemon_token
WHERE workspace_id = $1 AND daemon_id = $2;
