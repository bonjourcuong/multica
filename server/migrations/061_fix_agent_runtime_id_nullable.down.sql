-- Reverts 061_fix_agent_runtime_id_nullable.
--
-- The ALTER will fail if any agent row has runtime_id = NULL (typically
-- the V1 "Cuong Pho" twin agents). Operators rolling back must first
-- decide what to do with those rows — deleting them also drops the
-- global_chat_session that references them, since the FK is non-cascading.

BEGIN;

ALTER TABLE agent ALTER COLUMN runtime_id SET NOT NULL;

COMMIT;
