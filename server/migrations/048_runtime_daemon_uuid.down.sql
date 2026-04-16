DROP INDEX IF EXISTS idx_agent_runtime_legacy_daemon_id;
ALTER TABLE agent_runtime DROP COLUMN IF EXISTS legacy_daemon_id;
