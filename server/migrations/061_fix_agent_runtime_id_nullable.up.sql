-- Hotfix MUL-141: relax NOT NULL on agent.runtime_id.
--
-- The V1 "Cuong Pho" global twin is runtime-less by design — it is a
-- pure orchestrator that fans out work via cross_ws_query /
-- cross_ws_dispatch and never binds to a daemon. service.ensureGlobalAgent
-- therefore inserts the row with runtime_id = NULL. Under the original
-- 060_global_chat constraint that fails with SQLSTATE 23502, so EVERY
-- first-time POST /api/global/chat/sessions/me/messages returned a 500
-- and the FE surfaced "Could not send the message — try again in a moment."
--
-- The FK from agent.runtime_id to agent_runtime is preserved: any non-NULL
-- value still has to point at a real runtime row. Workspace agents and
-- the V3 Claude Code global agent both still set runtime_id; only the
-- runtime-less twin uses NULL.
--
-- Idempotent on a column that is already nullable, so this is safe to
-- ship even if a later migration drops the constraint again.

BEGIN;

ALTER TABLE agent ALTER COLUMN runtime_id DROP NOT NULL;

COMMIT;
