/**
 * Helpers for the global-chat V2 per-workspace lanes E2E specs (MUL-149,
 * carried over from the MUL-126 acceptance matrix).
 *
 * The fixture seeds two workspaces the default E2E user is a member of —
 * `e2e-v2-alpha` (with a `Pepper [WS]` agent) and `e2e-v2-beta` (with a
 * non-Pepper agent) — plus a third `e2e-v2-gamma` workspace owned by a
 * separate stranger user that the default user is NOT a member of, used
 * by the permission probe in case 8.
 *
 * `seedExtraLaneWorkspaces` provisions enough additional workspaces to
 * drive the LRU-eviction case (case 7). It's a separate call so cases that
 * don't need 13+ tiles don't pay the seeding cost.
 */

import "./env";
import pg from "pg";
import { type Page } from "@playwright/test";
import { TestApiClient } from "./fixtures";
import { getUserIdByEmail } from "./global-chat-helpers";

const DATABASE_URL =
  process.env.DATABASE_URL ??
  "postgres://multica:multica@localhost:5432/multica?sslmode=disable";

export const DEFAULT_E2E_EMAIL = "e2e@multica.ai";
const DEFAULT_E2E_NAME = "E2E User";

/** Member of alpha + beta. Pepper [WS] lives in alpha; beta has no Pepper. */
export const V2_ALPHA_SLUG = "e2e-v2-alpha";
const V2_ALPHA_NAME = "E2E V2 Alpha";
export const V2_BETA_SLUG = "e2e-v2-beta";
const V2_BETA_NAME = "E2E V2 Beta";

/** Owned by a stranger user; the default user is NOT a member. Case 8. */
export const V2_GAMMA_SLUG = "e2e-v2-gamma";
const V2_GAMMA_NAME = "E2E V2 Gamma";
export const V2_GAMMA_OWNER_EMAIL = "e2e-v2-gamma-owner@multica.ai";
const V2_GAMMA_OWNER_NAME = "E2E V2 Gamma Owner";

/** Pepper [WS] matches the ADR D5 default-agent regex. */
export const V2_ALPHA_AGENT_NAME = "Pepper [WS]";
/** Beta has a non-Pepper agent — ADR D5 falls through to first available. */
export const V2_BETA_AGENT_NAME = "Beta Bot";

/** Prefix for the LRU eviction workspaces (case 7). */
export const V2_LANE_PREFIX = "e2e-v2-lane";

export interface V2LaneFixture {
  defaultUserId: string;
  alpha: { id: string; slug: string; name: string; agentId: string };
  beta: { id: string; slug: string; name: string; agentId: string };
  gamma: { id: string; slug: string; name: string; ownerUserId: string };
}

/**
 * Idempotently seed alpha / beta / gamma. Runs at the start of every test
 * that needs the V2 lane fixtures so the data is consistent regardless of
 * what previous tests did to the user.
 *
 * The default user is force-(re)added to alpha + beta and force-removed
 * from gamma — the latter is the contract case 8 depends on.
 */
export async function seedV2LaneFixture(): Promise<V2LaneFixture> {
  // Make sure the default user exists — login also bootstraps the row in
  // `user`, which the membership inserts below depend on.
  const defaultApi = new TestApiClient();
  await defaultApi.login(DEFAULT_E2E_EMAIL, DEFAULT_E2E_NAME);
  const defaultUserId = await getUserIdByEmail(DEFAULT_E2E_EMAIL);
  if (!defaultUserId) {
    throw new Error(`could not resolve user id for ${DEFAULT_E2E_EMAIL}`);
  }

  const gammaApi = new TestApiClient();
  await gammaApi.login(V2_GAMMA_OWNER_EMAIL, V2_GAMMA_OWNER_NAME);
  const gammaOwnerId = await getUserIdByEmail(V2_GAMMA_OWNER_EMAIL);
  if (!gammaOwnerId) {
    throw new Error(`could not resolve user id for ${V2_GAMMA_OWNER_EMAIL}`);
  }

  const client = new pg.Client(DATABASE_URL);
  await client.connect();
  try {
    const alphaId = await ensureWorkspace(client, V2_ALPHA_SLUG, V2_ALPHA_NAME);
    const betaId = await ensureWorkspace(client, V2_BETA_SLUG, V2_BETA_NAME);
    const gammaId = await ensureWorkspace(client, V2_GAMMA_SLUG, V2_GAMMA_NAME);

    await ensureMembership(client, defaultUserId, alphaId, "owner");
    await ensureMembership(client, defaultUserId, betaId, "owner");
    await ensureMembership(client, gammaOwnerId, gammaId, "owner");
    // gamma must NOT have the default user — case 8 asserts the spoofed
    // membership probe is rejected. Defensive delete in case a previous
    // run left the row behind.
    await client.query(
      "DELETE FROM member WHERE user_id = $1 AND workspace_id = $2",
      [defaultUserId, gammaId],
    );

    const alphaAgentId = await ensureAgent(
      client,
      alphaId,
      V2_ALPHA_AGENT_NAME,
      defaultUserId,
    );
    const betaAgentId = await ensureAgent(
      client,
      betaId,
      V2_BETA_AGENT_NAME,
      defaultUserId,
    );

    return {
      defaultUserId,
      alpha: {
        id: alphaId,
        slug: V2_ALPHA_SLUG,
        name: V2_ALPHA_NAME,
        agentId: alphaAgentId,
      },
      beta: {
        id: betaId,
        slug: V2_BETA_SLUG,
        name: V2_BETA_NAME,
        agentId: betaAgentId,
      },
      gamma: {
        id: gammaId,
        slug: V2_GAMMA_SLUG,
        name: V2_GAMMA_NAME,
        ownerUserId: gammaOwnerId,
      },
    };
  } finally {
    await client.end();
  }
}

export interface ExtraLaneWorkspace {
  id: string;
  slug: string;
  name: string;
}

/**
 * Provision `count` additional workspaces the default user is a member of.
 * Used by case 7 (LRU eviction) to push the rail past
 * MAX_OPEN_WORKSPACE_LANES (12) and verify the oldest entry is dropped.
 *
 * Slugs are deterministic (`e2e-v2-lane-01`, ...) so re-running the test
 * is idempotent.
 */
export async function seedExtraLaneWorkspaces(
  defaultUserId: string,
  count: number,
): Promise<ExtraLaneWorkspace[]> {
  const client = new pg.Client(DATABASE_URL);
  await client.connect();
  try {
    const out: ExtraLaneWorkspace[] = [];
    for (let i = 1; i <= count; i += 1) {
      const slug = `${V2_LANE_PREFIX}-${i.toString().padStart(2, "0")}`;
      const name = `E2E V2 Lane ${i.toString().padStart(2, "0")}`;
      const wsId = await ensureWorkspace(client, slug, name);
      await ensureMembership(client, defaultUserId, wsId, "owner");
      out.push({ id: wsId, slug, name });
    }
    return out;
  } finally {
    await client.end();
  }
}

/**
 * Log in as the default E2E user and inject the JWT into localStorage so
 * subsequent navigations are authenticated. Returns the JWT for callers
 * that need to make their own API calls (case 8).
 */
export async function loginDefaultUser(page: Page): Promise<string> {
  const api = new TestApiClient();
  await api.login(DEFAULT_E2E_EMAIL, DEFAULT_E2E_NAME);
  const token = api.getToken();
  if (!token) throw new Error("expected JWT for default E2E user");
  await page.goto("/login");
  await page.evaluate((t) => {
    localStorage.setItem("multica_token", t);
  }, token);
  return token;
}

async function ensureWorkspace(
  client: pg.Client,
  slug: string,
  name: string,
): Promise<string> {
  const res = await client.query<{ id: string }>(
    `INSERT INTO workspace (name, slug, description)
     VALUES ($1, $2, 'E2E V2 lanes fixture')
     ON CONFLICT (slug) DO UPDATE
       SET name = EXCLUDED.name,
           updated_at = now()
     RETURNING id`,
    [name, slug],
  );
  return res.rows[0]!.id;
}

async function ensureMembership(
  client: pg.Client,
  userId: string,
  workspaceId: string,
  role: "owner" | "admin" | "member",
) {
  await client.query(
    `INSERT INTO member (workspace_id, user_id, role)
     VALUES ($1, $2, $3)
     ON CONFLICT (workspace_id, user_id) DO UPDATE SET role = EXCLUDED.role`,
    [workspaceId, userId, role],
  );
}

/**
 * Idempotent agent insert. visibility=workspace so canAssignAgent returns
 * true for any member; runtime_mode=cloud + runtime_id NULL is what the
 * V1 helper already does (see global-chat-helpers.ensureWorkspaceAgent).
 */
async function ensureAgent(
  client: pg.Client,
  workspaceId: string,
  name: string,
  ownerId: string,
): Promise<string> {
  const found = await client.query<{ id: string }>(
    "SELECT id FROM agent WHERE workspace_id = $1 AND name = $2 LIMIT 1",
    [workspaceId, name],
  );
  if (found.rows[0]?.id) {
    return found.rows[0].id;
  }
  const inserted = await client.query<{ id: string }>(
    `INSERT INTO agent (workspace_id, name, description, runtime_mode, scope, visibility, owner_id)
     VALUES ($1, $2, '', 'cloud', 'workspace', 'workspace', $3)
     RETURNING id`,
    [workspaceId, name, ownerId],
  );
  return inserted.rows[0]!.id;
}

/**
 * Hard-reset the persisted V2 lane / chat state in localStorage. The lanes
 * store and the chat store both bleed across spec runs (same browser
 * profile, same hostname) and a leftover entry can mask a regression — eg.
 * a stale `byWorkspace[alphaId].selectedAgentId` would make case 1 pass
 * even if the lane no longer wires the agent picker correctly.
 */
export async function clearLaneAndChatStorage(page: Page) {
  await page.evaluate(() => {
    localStorage.removeItem("multica.global-chat.v2.lanes");
    localStorage.removeItem("multica:chat:byWorkspace");
    localStorage.removeItem("multica:chat:drafts");
  });
}
