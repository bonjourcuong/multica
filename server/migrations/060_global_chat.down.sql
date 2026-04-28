-- Reverse migration for global orchestrator chat schema (MUL-31).
--
-- IMPORTANT: re-asserting `agent.workspace_id NOT NULL` will fail if any
-- global-scope agent rows still exist. Operators must manually delete the
-- offending rows before rolling back:
--
--   DELETE FROM agent WHERE scope = 'global';
--
-- Down does NOT silently destroy global agents — surfacing the failure is
-- intentional so the operator confirms data loss explicitly.

BEGIN;

DROP INDEX IF EXISTS idx_global_chat_message_session;
DROP TABLE IF EXISTS global_chat_message;
DROP TABLE IF EXISTS global_chat_session;

DROP INDEX IF EXISTS uniq_global_agent_per_user;
ALTER TABLE agent DROP CONSTRAINT IF EXISTS agent_scope_owner;
ALTER TABLE agent ALTER COLUMN workspace_id SET NOT NULL;
ALTER TABLE agent DROP COLUMN IF EXISTS user_id;
ALTER TABLE agent DROP COLUMN IF EXISTS scope;

ALTER TABLE chat_message DROP COLUMN IF EXISTS metadata;

DROP INDEX IF EXISTS idx_chat_session_scope_workspace;
ALTER TABLE chat_session DROP COLUMN IF EXISTS scope;

COMMIT;
