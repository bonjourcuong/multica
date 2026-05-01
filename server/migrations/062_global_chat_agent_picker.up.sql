-- Global Chat V3 — agent picker (MUL-137).
--
-- Two purely additive schema changes that unlock the per-user agent picker
-- on the global lane:
--
--   1. Drop the unique index that limited a user to a single global agent.
--      The lookup invariant (scope='global', user_id, name) is now enforced
--      at the service layer (EnsureClaudeCodeGlobalAgent) instead of via
--      a DB constraint, because we need both the legacy "Cuong Pho" twin
--      AND the new "Claude Code (terminator-9999)" agent to coexist for
--      the same user. The agent_scope_owner CHECK constraint stays — it
--      still pins (scope='global' ⇒ user_id NOT NULL AND workspace_id NULL).
--
--   2. Add a `global_session_id` foreign key to agent_task_queue so a
--      global-chat-triggered run can be linked back to its source session
--      the same way chat tasks are linked to chat_session. This is the
--      first time the global lane actually triggers a runtime (V1 only
--      did mention fan-out). Reuse of the existing chat-task callback path
--      (TaskService.CompleteTask) means the daemon writes back agent
--      replies via `/api/daemon/tasks/{id}/complete`; no new endpoint.
--
-- See ADR `2026-05-01-global-chat-v3-agent-picker.md`.

BEGIN;

-- 1. Allow N global agents per user. The lookup index keeps reads on
--    `scope = 'global'` cheap; uniqueness is gone.
DROP INDEX IF EXISTS uniq_global_agent_per_user;
CREATE INDEX idx_agent_global_by_user
    ON agent(user_id) WHERE scope = 'global';

-- 2. New task target column for global-chat-triggered runs. NULL for
--    every existing row; populated by the new EnqueueGlobalChatTask path.
--    ON DELETE CASCADE matches chat_session_id semantics: deleting a
--    global session also drops its in-flight tasks.
ALTER TABLE agent_task_queue
    ADD COLUMN global_session_id UUID
    REFERENCES global_chat_session(id) ON DELETE CASCADE;

CREATE INDEX idx_agent_task_queue_global_session
    ON agent_task_queue(global_session_id) WHERE global_session_id IS NOT NULL;

COMMIT;
