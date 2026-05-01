/**
 * Helpers for the cross-workspace meta view E2E specs (MUL-7).
 *
 * The `/global` Kanban aggregates data by membership, so the fixture creates
 * dedicated E2E users and workspaces in Postgres. That keeps the browser flow
 * realistic while avoiding leakage from other E2E specs that use the default
 * `e2e@multica.ai` account.
 */

import "./env";
import pg from "pg";
import { type Page } from "@playwright/test";
import { TestApiClient } from "./fixtures";
import { getUserIdByEmail } from "./global-chat-helpers";

const DATABASE_URL =
  process.env.DATABASE_URL ??
  "postgres://multica:multica@localhost:5432/multica?sslmode=disable";

export const META_USER_EMAIL = "e2e-meta@multica.ai";
const META_USER_NAME = "E2E Meta User";

export const LIMITED_USER_EMAIL = "e2e-meta-limited@multica.ai";
const LIMITED_USER_NAME = "E2E Meta Limited";

export const META_STATUSES = [
  "backlog",
  "todo",
  "in_progress",
  "in_review",
  "done",
] as const;

export interface MetaWorkspaceFixture {
  id: string;
  name: string;
  slug: string;
  issuePrefix: string;
}

export interface SeededMetaIssue {
  id: string;
  title: string;
  status: (typeof META_STATUSES)[number];
  workspace: MetaWorkspaceFixture;
}

export interface MetaFixture {
  token: string;
  userId: string;
  workspaces: MetaWorkspaceFixture[];
  issues: SeededMetaIssue[];
}

const META_WORKSPACES: MetaWorkspaceFixture[] = [
  {
    id: "",
    name: "E2E Meta Alpha",
    slug: "e2e-meta-alpha",
    issuePrefix: "EMA",
  },
  {
    id: "",
    name: "E2E Meta Beta",
    slug: "e2e-meta-beta",
    issuePrefix: "EMB",
  },
  {
    id: "",
    name: "E2E Meta Gamma",
    slug: "e2e-meta-gamma",
    issuePrefix: "EMG",
  },
];

const LIMITED_WORKSPACE: MetaWorkspaceFixture = {
  id: "",
  name: "E2E Meta Solo",
  slug: "e2e-meta-solo",
  issuePrefix: "EMS",
};

export async function loginSeededMetaUser(page: Page): Promise<MetaFixture> {
  const api = new TestApiClient();
  await api.login(META_USER_EMAIL, META_USER_NAME);
  const token = api.getToken();
  if (!token) throw new Error("expected token for meta E2E user");

  const userId = await getUserIdByEmail(META_USER_EMAIL);
  if (!userId) throw new Error(`could not resolve user id for ${META_USER_EMAIL}`);

  const fixture = await seedMetaFixture(userId, META_WORKSPACES);
  await injectToken(page, token);
  return { token, userId, ...fixture };
}

export async function loginLimitedMetaUser(page: Page): Promise<MetaFixture> {
  const api = new TestApiClient();
  await api.login(LIMITED_USER_EMAIL, LIMITED_USER_NAME);
  const token = api.getToken();
  if (!token) throw new Error("expected token for limited meta E2E user");

  const userId = await getUserIdByEmail(LIMITED_USER_EMAIL);
  if (!userId) {
    throw new Error(`could not resolve user id for ${LIMITED_USER_EMAIL}`);
  }

  const fixture = await seedMetaFixture(userId, [LIMITED_WORKSPACE]);
  await injectToken(page, token);
  return { token, userId, ...fixture };
}

/**
 * Create an issue through the public API so the handler publishes
 * `issue:created` on the workspace's WS scope. A direct DB insert bypasses
 * the event bus and would never reach a connected client — fine for seeding
 * static fixtures, but useless when the test asserts realtime fan-out.
 */
export async function createRealtimeIssue(
  workspace: MetaWorkspaceFixture,
  token: string,
  title: string,
) {
  const apiBase =
    process.env.NEXT_PUBLIC_API_URL ||
    `http://localhost:${process.env.PORT || "8080"}`;
  const res = await fetch(`${apiBase}/api/issues`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-Workspace-Slug": workspace.slug,
      Authorization: `Bearer ${token}`,
    },
    body: JSON.stringify({ title }),
  });
  if (!res.ok) {
    throw new Error(
      `POST /api/issues failed for ${workspace.slug}: ${res.status}`,
    );
  }
  const data = (await res.json()) as { id: string };
  return data.id;
}

export async function listCrossWorkspaceIssues(
  token: string,
  params = "",
): Promise<{ issues: SeededMetaIssue[]; total_returned: number }> {
  const apiBase =
    process.env.NEXT_PUBLIC_API_URL ||
    `http://localhost:${process.env.PORT || "8080"}`;
  const suffix = params ? `?${params}` : "";
  const res = await fetch(`${apiBase}/api/issues/cross-workspace${suffix}`, {
    headers: { Authorization: `Bearer ${token}` },
  });
  if (!res.ok) {
    throw new Error(`GET /api/issues/cross-workspace failed: ${res.status}`);
  }
  return (await res.json()) as {
    issues: SeededMetaIssue[];
    total_returned: number;
  };
}

async function injectToken(page: Page, token: string) {
  await page.goto("/login");
  await page.evaluate((t) => {
    localStorage.setItem("multica_token", t);
  }, token);
}

async function seedMetaFixture(
  userId: string,
  workspaceSpecs: MetaWorkspaceFixture[],
): Promise<Omit<MetaFixture, "token" | "userId">> {
  const client = new pg.Client(DATABASE_URL);
  await client.connect();
  try {
    const slugs = workspaceSpecs.map((ws) => ws.slug);
    await client.query(
      `DELETE FROM member
       WHERE user_id = $1
         AND workspace_id NOT IN (SELECT id FROM workspace WHERE slug = ANY($2::text[]))`,
      [userId, slugs],
    );

    const workspaces: MetaWorkspaceFixture[] = [];
    const issues: SeededMetaIssue[] = [];

    for (const spec of workspaceSpecs) {
      const ws = await ensureWorkspace(client, spec);
      workspaces.push(ws);
      await ensureMembership(client, userId, ws.id);
      await resetWorkspaceIssues(client, ws.id);

      for (let i = 0; i < 10; i += 1) {
        const status = META_STATUSES[i % META_STATUSES.length];
        const title = `E2E Meta ${ws.issuePrefix} ${status} ${i + 1}`;
        const result = await client.query<{ id: string }>(
          `INSERT INTO issue (
             workspace_id, title, status, priority, creator_type, creator_id,
             number, created_at, updated_at
           ) VALUES ($1, $2, $3, $4, 'member', $5, $6, now() - ($7::int * interval '1 minute'), now())
           RETURNING id`,
          [
            ws.id,
            title,
            status,
            i % 2 === 0 ? "medium" : "low",
            userId,
            i + 1,
            i,
          ],
        );
        issues.push({ id: result.rows[0]!.id, title, status, workspace: ws });
      }
      await client.query("UPDATE workspace SET issue_counter = 10 WHERE id = $1", [
        ws.id,
      ]);
    }

    return { workspaces, issues };
  } finally {
    await client.end();
  }
}

async function ensureWorkspace(
  client: pg.Client,
  spec: MetaWorkspaceFixture,
): Promise<MetaWorkspaceFixture> {
  const result = await client.query<{ id: string }>(
    `INSERT INTO workspace (name, slug, description, issue_prefix)
     VALUES ($1, $2, 'E2E cross-workspace meta view fixture', $3)
     ON CONFLICT (slug) DO UPDATE
       SET name = EXCLUDED.name,
           issue_prefix = EXCLUDED.issue_prefix,
           updated_at = now()
     RETURNING id`,
    [spec.name, spec.slug, spec.issuePrefix],
  );
  return { ...spec, id: result.rows[0]!.id };
}

async function ensureMembership(
  client: pg.Client,
  userId: string,
  workspaceId: string,
) {
  await client.query(
    `INSERT INTO member (workspace_id, user_id, role)
     VALUES ($1, $2, 'owner')
     ON CONFLICT (workspace_id, user_id) DO UPDATE SET role = 'owner'`,
    [workspaceId, userId],
  );
}

async function resetWorkspaceIssues(client: pg.Client, workspaceId: string) {
  await client.query("DELETE FROM issue WHERE workspace_id = $1", [workspaceId]);
  await client.query("UPDATE workspace SET issue_counter = 0 WHERE id = $1", [
    workspaceId,
  ]);
}

