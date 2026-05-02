DROP INDEX IF EXISTS idx_daemon_token_user;

ALTER TABLE daemon_token
    DROP COLUMN IF EXISTS user_id;
