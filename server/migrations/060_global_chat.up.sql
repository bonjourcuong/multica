-- Global Orchestrator Chat schema (Phase 1, MUL-31).
--
-- Adds:
--   1. `chat_session.scope` to discriminate workspace chats from per-workspace
--      "Cuong Global" mirror sessions.
--   2. `chat_message.metadata` so mirror messages can carry a
--      `global_origin` pointer back to the originating global message.
--   3. `agent.scope` + `agent.user_id` and relaxes `agent.workspace_id` so a
--      single global "Cuong Pho" agent can exist per user, outside any
--      workspace.
--   4. New tables `global_chat_session` (one per user) and
--      `global_chat_message` for the user-facing global stream.
--
-- See ADR `2026-04-28-global-orchestrator-chat.md` and spec
-- `2026-04-28-global-orchestrator-chat-design.md`.

BEGIN;

-- 1. chat_session: add `scope`.
ALTER TABLE chat_session
    ADD COLUMN scope TEXT NOT NULL DEFAULT 'workspace'
        CHECK (scope IN ('workspace', 'global_mirror'));

CREATE INDEX idx_chat_session_scope_workspace
    ON chat_session(workspace_id, scope);

-- 2. chat_message: add `metadata` for global_origin pointer.
ALTER TABLE chat_message
    ADD COLUMN metadata JSONB NOT NULL DEFAULT '{}';

-- 3. agent: add `scope`, `user_id`, relax `workspace_id`, enforce invariant.
ALTER TABLE agent
    ADD COLUMN scope TEXT NOT NULL DEFAULT 'workspace'
        CHECK (scope IN ('workspace', 'global'));

ALTER TABLE agent
    ADD COLUMN user_id UUID REFERENCES "user"(id) ON DELETE CASCADE;

ALTER TABLE agent
    ALTER COLUMN workspace_id DROP NOT NULL;

-- Enforce: workspace agents must have workspace_id and no user_id;
-- global agents must have user_id and no workspace_id.
ALTER TABLE agent
    ADD CONSTRAINT agent_scope_owner CHECK (
        (scope = 'workspace' AND workspace_id IS NOT NULL AND user_id IS NULL)
        OR
        (scope = 'global' AND user_id IS NOT NULL AND workspace_id IS NULL)
    );

CREATE UNIQUE INDEX uniq_global_agent_per_user
    ON agent(user_id) WHERE scope = 'global';

-- 4. global_chat_session: one per user (digital-twin orchestrator session).
CREATE TABLE global_chat_session (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agent(id),
    title TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    archived_at TIMESTAMPTZ,
    UNIQUE (user_id)
);

CREATE TABLE global_chat_message (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    global_session_id UUID NOT NULL REFERENCES global_chat_session(id) ON DELETE CASCADE,
    author_kind TEXT NOT NULL CHECK (author_kind IN ('user', 'agent')),
    author_id UUID NOT NULL,
    body TEXT NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}',
    -- shape: [{workspace_id, mirror_session_id, mirror_message_id}, ...]
    dispatched_to JSONB NOT NULL DEFAULT '[]',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_global_chat_message_session
    ON global_chat_message(global_session_id, created_at DESC);

COMMIT;
