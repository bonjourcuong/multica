-- MUL-191 (Finding #7): bind every daemon_token row to the user that
-- minted it. The owning user is the missing piece needed by Finding #1's
-- runtime-ownership check in requireDaemonRuntimeAccess; without it a
-- co-member who holds any valid mdt_ token for the workspace can claim
-- another member's runtime.
--
-- No backfill: the daemon-login flow that mints these rows is not yet
-- wired in production (CreateDaemonToken has no caller), so the table is
-- expected to be empty. We still TRUNCATE first to keep the migration
-- idempotent and avoid forging a synthetic user_id for any stray rows;
-- daemons whose tokens get cleared simply re-login on next start.
TRUNCATE TABLE daemon_token;

ALTER TABLE daemon_token
    ADD COLUMN user_id uuid NOT NULL REFERENCES "user"(id) ON DELETE CASCADE;

CREATE INDEX idx_daemon_token_user ON daemon_token(user_id);
