-- Reverse migration for global chat V3 — agent picker (MUL-137).
--
-- IMPORTANT: re-asserting the unique index on (user_id) WHERE scope='global'
-- will fail if any user has more than one global agent — which is the
-- expected steady state after V3 ships (Cuong Pho twin + Claude Code).
-- Down does NOT silently drop the extra agents; it raises EXCEPTION so the
-- operator confirms the destructive consolidation explicitly:
--
--   DELETE FROM agent
--    WHERE scope = 'global'
--      AND id NOT IN (
--          SELECT DISTINCT ON (user_id) id
--          FROM agent
--          WHERE scope = 'global'
--          ORDER BY user_id, created_at ASC
--      );
--
-- Run that (or the equivalent reconciliation) FIRST, then re-run the
-- down migration. The same explicit-fail pattern as 060 — surfacing the
-- inconsistency is intentional.

BEGIN;

DROP INDEX IF EXISTS idx_agent_task_queue_global_session;
ALTER TABLE agent_task_queue DROP COLUMN IF EXISTS global_session_id;

DROP INDEX IF EXISTS idx_agent_global_by_user;

DO $$
DECLARE
    duplicate_user UUID;
BEGIN
    SELECT user_id INTO duplicate_user
    FROM agent
    WHERE scope = 'global'
    GROUP BY user_id
    HAVING COUNT(*) > 1
    LIMIT 1;

    IF duplicate_user IS NOT NULL THEN
        RAISE EXCEPTION
            'cannot restore uniq_global_agent_per_user: user % has multiple global agents; consolidate manually before rolling back (see migration header for SQL)',
            duplicate_user;
    END IF;
END $$;

CREATE UNIQUE INDEX uniq_global_agent_per_user
    ON agent(user_id) WHERE scope = 'global';

COMMIT;
