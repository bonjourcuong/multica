/**
 * Helpers for the global orchestrator chat E2E specs (MUL-33).
 *
 * The global chat lives outside any single workspace, so these tests need:
 *   - a way to log in as a *secondary* "stranger" user that owns a workspace
 *     the default E2E user is NOT a member of (permissions test).
 *   - direct DB access to confirm that membership-rejected dispatches leave
 *     zero side-effects in the stranger workspace (ADR risk R5 audit).
 *   - a sanity-seed for at least one workspace agent so the happy-path
 *     dispatch can resolve a host for the per-user "global_mirror" session.
 */

import "./env";
import pg from "pg";
import { TestApiClient } from "./fixtures";

const DATABASE_URL =
  process.env.DATABASE_URL ??
  "postgres://multica:multica@localhost:5432/multica?sslmode=disable";

/** Dedicated user used as the "stranger" workspace owner in T16. */
export const STRANGER_USER_EMAIL = "e2e-stranger@multica.ai";
const STRANGER_USER_NAME = "E2E Stranger";

/** Slug used for the stranger workspace. Stable across runs so we re-use. */
export const STRANGER_WORKSPACE_SLUG = "e2e-stranger";
const STRANGER_WORKSPACE_NAME = "E2E Stranger Workspace";

export interface StrangerWorkspace {
  workspace_id: string;
  workspace_slug: string;
  owner_email: string;
}

/**
 * Logs in as the stranger user and ensures a workspace they own exists. The
 * default E2E user is intentionally NOT a member of this workspace.
 */
export async function ensureStrangerWorkspace(): Promise<StrangerWorkspace> {
  const stranger = new TestApiClient();
  await stranger.login(STRANGER_USER_EMAIL, STRANGER_USER_NAME);
  const ws = await stranger.ensureWorkspace(
    STRANGER_WORKSPACE_NAME,
    STRANGER_WORKSPACE_SLUG,
  );
  return {
    workspace_id: ws.id,
    workspace_slug: ws.slug,
    owner_email: STRANGER_USER_EMAIL,
  };
}

/** Returns the auth user UUID for an email, or null if unknown. */
export async function getUserIdByEmail(email: string): Promise<string | null> {
  const client = new pg.Client(DATABASE_URL);
  await client.connect();
  try {
    const r = await client.query<{ id: string }>(
      'SELECT id FROM "user" WHERE email = $1 LIMIT 1',
      [email],
    );
    return r.rows[0]?.id ?? null;
  } finally {
    await client.end();
  }
}

/** Resolves a workspace UUID by slug. */
export async function getWorkspaceIdBySlug(slug: string): Promise<string | null> {
  const client = new pg.Client(DATABASE_URL);
  await client.connect();
  try {
    const r = await client.query<{ id: string }>(
      "SELECT id FROM workspace WHERE slug = $1 LIMIT 1",
      [slug],
    );
    return r.rows[0]?.id ?? null;
  } finally {
    await client.end();
  }
}

/**
 * Counts global-mirror chat_session rows for a given (workspace, creator)
 * pair. Used by the permissions spec to assert ZERO mirror sessions exist
 * for an unauthorized dispatch — the membership filter is supposed to
 * reject the request before any write happens (ADR risk R5).
 */
export async function countMirrorSessions(
  workspaceId: string,
  creatorUserId: string,
): Promise<number> {
  const client = new pg.Client(DATABASE_URL);
  await client.connect();
  try {
    const r = await client.query<{ n: string }>(
      `SELECT COUNT(*)::text AS n
         FROM chat_session
        WHERE workspace_id = $1
          AND scope = 'global_mirror'
          AND creator_id = $2`,
      [workspaceId, creatorUserId],
    );
    return Number(r.rows[0]?.n ?? "0");
  } finally {
    await client.end();
  }
}

/**
 * Counts mirror chat_message rows whose body contains the marker, scoped to
 * the given workspace. Catches the rare case where a write somehow lands on
 * a pre-existing mirror session despite a membership reject.
 */
export async function countMirrorMessagesContaining(
  workspaceId: string,
  bodyMarker: string,
): Promise<number> {
  const client = new pg.Client(DATABASE_URL);
  await client.connect();
  try {
    const r = await client.query<{ n: string }>(
      `SELECT COUNT(*)::text AS n
         FROM chat_message m
         JOIN chat_session s ON s.id = m.chat_session_id
        WHERE s.workspace_id = $1
          AND s.scope = 'global_mirror'
          AND m.content LIKE $2`,
      [workspaceId, `%${bodyMarker}%`],
    );
    return Number(r.rows[0]?.n ?? "0");
  } finally {
    await client.end();
  }
}

/**
 * Ensures the workspace has at least one workspace-scope agent so
 * GlobalDispatchService.resolveMirrorAgentID returns a host for the mirror
 * session. The dispatch fails outright when there are zero workspace agents,
 * which would mask the membership/realtime contract this test is asserting.
 *
 * Idempotent: re-running leaves a single seeded agent.
 */
export async function ensureWorkspaceAgent(
  workspaceId: string,
  name = "E2E Mirror Host",
): Promise<void> {
  const client = new pg.Client(DATABASE_URL);
  await client.connect();
  try {
    await client.query(
      `INSERT INTO agent (workspace_id, name, runtime_mode, scope)
       SELECT $1, $2, 'cloud', 'workspace'
       WHERE NOT EXISTS (
         SELECT 1 FROM agent WHERE workspace_id = $1 AND name = $2
       )`,
      [workspaceId, name],
    );
  } finally {
    await client.end();
  }
}

/**
 * One row of `GET /api/global/chat/mirrors`. Mirrors `GlobalMirrorSummary`
 * in `packages/core/types/global-chat.ts`; duplicated here so the E2E
 * package stays decoupled from the app build graph.
 */
export interface MirrorSummaryRow {
  workspace_id: string;
  workspace_slug: string;
  workspace_name: string;
  mirror_session_id: string | null;
  last_message_at: string | null;
  unread_count: number;
}

/**
 * Fetches the mirrors endpoint authenticated as the given JWT. Used by the
 * happy-path spec to assert that, after a dispatch, the calling user's
 * mirror summary exposes the freshly-created `mirror_session_id` and a
 * non-null `last_message_at` — i.e. the contract the tile grid was coded
 * against actually works on the wire.
 */
export async function listGlobalMirrors(
  token: string,
): Promise<MirrorSummaryRow[]> {
  const apiBase =
    process.env.NEXT_PUBLIC_API_URL ||
    `http://localhost:${process.env.PORT || "8080"}`;
  const res = await fetch(`${apiBase}/api/global/chat/mirrors`, {
    headers: { Authorization: `Bearer ${token}` },
  });
  if (!res.ok) {
    throw new Error(`GET /api/global/chat/mirrors failed: ${res.status}`);
  }
  return (await res.json()) as MirrorSummaryRow[];
}
