/**
 * MUL-149 — Global chat V2 per-workspace lanes regression spec.
 *
 * Carries the e2e cases originally enumerated under MUL-126 (V2 merge gate).
 * The acceptance matrix lives in the issue body; this file lands cases 1–3
 * and 5–8 as live tests, plus case 4 as a deferred-skip pointing at
 * MUL-31 (realtime hub). See the V2 ADR
 * (`PKM-CUONG/GROWTH/PROJECTS/multica-fork/adrs/2026-05-01-global-chat-v2-multiplexer.md`)
 * for the contracts asserted here:
 *   D5  default agent picker priority (Pepper match → first available)
 *   D8  background lane LRU cap at MAX_OPEN_WORKSPACE_LANES (12)
 *   R1  per-workspace chat-store entries; reload restores per-lane state
 *   R5  membership filter on cross-workspace dispatch / find-or-create
 *
 * The membership filter on POST /api/chat/sessions/find-or-create returns
 * 404 (existence-hiding lockdown — see MUL-132 / `RequireWorkspaceMember`),
 * not 403 as the original DoD wrote. Case 8 codifies the production
 * contract instead of the spec text — a 403 here would be the regression
 * to flag.
 */

import { test, expect, type Page } from "@playwright/test";
import {
  V2_ALPHA_AGENT_NAME,
  V2_BETA_AGENT_NAME,
  V2_GAMMA_SLUG,
  clearLaneAndChatStorage,
  loginDefaultUser,
  seedExtraLaneWorkspaces,
  seedV2LaneFixture,
  type V2LaneFixture,
} from "./global-chat-v2-lanes-helpers";

const API_BASE =
  process.env.NEXT_PUBLIC_API_URL ||
  `http://localhost:${process.env.PORT || "8080"}`;

test.describe("Global chat V2 — per-workspace lanes", () => {
  let fixture: V2LaneFixture;
  let token: string;

  test.beforeEach(async ({ page }) => {
    fixture = await seedV2LaneFixture();
    token = await loginDefaultUser(page);
  });

  // -------------------------------------------------------------------------
  // Case 1 — Click alpha tile → lane opens, active, agent picker = Pepper [WS].
  // -------------------------------------------------------------------------
  test("case 1: clicking alpha tile opens an active lane with the Pepper [WS] picker", async ({
    page,
  }) => {
    await page.goto("/global/chat");
    await clearLaneAndChatStorage(page);
    await page.reload();

    await openLaneByTile(page, fixture.alpha.slug);

    const lane = laneRailItem(page, fixture.alpha.id);
    await expect(lane).toBeVisible();
    await expect(lane).toHaveAttribute("data-active", "true");

    await expect(activeLaneAgentPicker(page, fixture.alpha.id)).toHaveText(
      V2_ALPHA_AGENT_NAME,
    );
  });

  // -------------------------------------------------------------------------
  // Case 2 — Click beta tile → lane opens, agent picker = beta's first agent
  //          (no Pepper there). Same flow, different default-agent branch.
  // -------------------------------------------------------------------------
  test("case 2: clicking beta tile opens a lane whose picker falls through to the first available agent", async ({
    page,
  }) => {
    await page.goto("/global/chat");
    await clearLaneAndChatStorage(page);
    await page.reload();

    await openLaneByTile(page, fixture.beta.slug);

    const lane = laneRailItem(page, fixture.beta.id);
    await expect(lane).toBeVisible();
    await expect(lane).toHaveAttribute("data-active", "true");

    const picker = activeLaneAgentPicker(page, fixture.beta.id);
    await expect(picker).toHaveText(V2_BETA_AGENT_NAME);
    await expect(picker).not.toHaveText(V2_ALPHA_AGENT_NAME);
  });

  // -------------------------------------------------------------------------
  // Case 3 — Sending in the alpha lane lands in the user's normal alpha chat
  //          session (find-or-create reuses it; POST /messages persists;
  //          GET /messages with X-Workspace-Slug returns it).
  //
  // Driven via the chat API rather than the contenteditable so the assertion
  // is on the wiring contract (workspace-slug routing, find-or-create
  // resume), not on the rich-text editor. The lane UI is then asserted to
  // pick up the persisted message after a reload — the lane chat queries
  // use staleTime=Infinity, so a same-session re-render does not refetch
  // and would silently mask a regression.
  // -------------------------------------------------------------------------
  test("case 3: sending in the alpha lane persists in the alpha chat session", async ({
    page,
  }) => {
    await page.goto("/global/chat");
    await clearLaneAndChatStorage(page);
    await page.reload();

    const marker = `E2E-V2-LANE-CASE3-${Date.now()}`;
    const session = await findOrCreateAlphaSession(token, fixture.alpha.agentId);
    await postChatMessage(token, fixture.alpha.slug, session.id, marker);

    // Wire contract: GET messages with the workspace-slug header returns
    // the body — same surface a freshly mounted lane uses to populate its
    // message list.
    const messages = await listChatMessages(token, fixture.alpha.slug, session.id);
    expect(messages.some((m) => m.content?.includes(marker))).toBe(true);

    // UI bound: open the alpha lane on a fresh page (caches are clean,
    // chatSessionsOptions has not yet hydrated for this workspace) and
    // confirm the lane's restore-most-recent-session effect surfaces the
    // body. This is the test-against-the-public-API path the DoD calls
    // out for case 3.
    await openLaneByTile(page, fixture.alpha.slug);
    const lane = workspaceLane(page, fixture.alpha.id);
    await expect(lane.getByText(marker)).toBeVisible({ timeout: 10000 });
  });

  // -------------------------------------------------------------------------
  // Case 4 — Realtime agent reply. Deferred to MUL-31 (the realtime hub
  //          rewrite); without it, an agent-authored message in the lane's
  //          session does not push to the open client. Skipped — keep the
  //          name in the matrix so re-enabling is a 1-line edit.
  // -------------------------------------------------------------------------
  test.skip("case 4: agent reply appears in the lane via realtime hub (unskip when MUL-31 ships)", async () => {
    // Intentional: realtime hub for cross-workspace lanes lands with
    // MUL-31. When that ships, replace this body with:
    //   - seed an assistant-authored chat_message in alpha's session
    //   - assert the lane subtree renders it without page.reload()
  });

  // -------------------------------------------------------------------------
  // Case 5 — Close lane, reopen by clicking tile again → same thread restored.
  //          find-or-create returns the existing session; messages survive.
  // -------------------------------------------------------------------------
  test("case 5: closing then reopening a lane resumes the same chat thread", async ({
    page,
  }) => {
    // Pre-seed the alpha session and a marker message BEFORE the lane is
    // ever opened so the lane's first render hydrates its session-list
    // cache from a state that already contains the row. Otherwise the
    // staleTime=Infinity cache from the empty initial fetch would mask
    // the post-close API call.
    const marker = `E2E-V2-LANE-CASE5-${Date.now()}`;
    const session = await findOrCreateAlphaSession(token, fixture.alpha.agentId);
    await postChatMessage(token, fixture.alpha.slug, session.id, marker);

    await page.goto("/global/chat");
    await clearLaneAndChatStorage(page);
    await page.reload();

    await openLaneByTile(page, fixture.alpha.slug);
    const lane = workspaceLane(page, fixture.alpha.id);
    await expect(lane.getByText(marker)).toBeVisible({ timeout: 10000 });

    // Close the alpha lane via the rail × button. opacity-0 → opacity-100
    // on hover only — Playwright dispatches a hover before the click so
    // the actionability check passes.
    await page
      .getByRole("button", { name: `Close ${fixture.alpha.name} lane` })
      .click();
    await expect(laneRailItem(page, fixture.alpha.id)).toHaveCount(0);

    // Reopen by clicking the tile again — the lane remounts and must
    // resume the same session.
    await openLaneByTile(page, fixture.alpha.slug);

    // find-or-create must have returned the SAME session id — verified by
    // both (a) the API call below resolving to the same row, and (b) the
    // marker still visible in the freshly remounted lane.
    const resumed = await findOrCreateAlphaSession(token, fixture.alpha.agentId);
    expect(resumed.id).toBe(session.id);

    const reopened = workspaceLane(page, fixture.alpha.id);
    await expect(reopened.getByText(marker)).toBeVisible({ timeout: 10000 });
  });

  // -------------------------------------------------------------------------
  // Case 6 — Reload the page → alpha + beta lanes both reopen; pickers persist.
  // -------------------------------------------------------------------------
  test("case 6: reloading restores both lanes and per-lane agent pickers", async ({
    page,
  }) => {
    await page.goto("/global/chat");
    await clearLaneAndChatStorage(page);
    await page.reload();

    await openLaneByTile(page, fixture.alpha.slug);
    await openLaneByTile(page, fixture.beta.slug);

    // Confirm both lanes are present pre-reload — sanity check, also lets
    // the debounced persist (100ms) flush before we navigate away.
    await expect(laneRailItem(page, fixture.alpha.id)).toBeVisible();
    await expect(laneRailItem(page, fixture.beta.id)).toBeVisible();
    await page.waitForTimeout(250);

    await page.reload();

    await expect(laneRailItem(page, fixture.alpha.id)).toBeVisible();
    await expect(laneRailItem(page, fixture.beta.id)).toBeVisible();

    // Activate each lane in turn and assert the picker resolves to the
    // workspace-specific agent. The picker is rendered into the visible
    // lane only, so we have to switch to read it.
    await laneRailItem(page, fixture.alpha.id).click();
    await expect(activeLaneAgentPicker(page, fixture.alpha.id)).toHaveText(
      V2_ALPHA_AGENT_NAME,
    );

    await laneRailItem(page, fixture.beta.id).click();
    await expect(activeLaneAgentPicker(page, fixture.beta.id)).toHaveText(
      V2_BETA_AGENT_NAME,
    );
  });

  // -------------------------------------------------------------------------
  // Case 7 — Open a 13th workspace lane → LRU evicts the oldest. Cap is 12
  //          (MAX_OPEN_WORKSPACE_LANES, ADR D8). The Global lane is implicit
  //          and does not count toward the cap.
  // -------------------------------------------------------------------------
  test("case 7: opening a 13th workspace lane LRU-evicts the oldest", async ({
    page,
  }) => {
    const extras = await seedExtraLaneWorkspaces(fixture.defaultUserId, 13);
    expect(extras).toHaveLength(13);

    await page.goto("/global/chat");
    await clearLaneAndChatStorage(page);
    await page.reload();

    // Open lanes 1..12. Each open hoists the lane to the front of the rail.
    for (let i = 0; i < 12; i += 1) {
      await openLaneByTile(page, extras[i]!.slug);
      await expect(laneRailItem(page, extras[i]!.id)).toBeVisible();
    }
    // Sanity: rail has exactly 12 workspace lanes (Global is implicit).
    await expect(workspaceLaneRailItems(page)).toHaveCount(12);

    // Open the 13th — the oldest (extras[0]) must be evicted.
    await openLaneByTile(page, extras[12]!.slug);
    await expect(laneRailItem(page, extras[12]!.id)).toBeVisible();
    await expect(workspaceLaneRailItems(page)).toHaveCount(12);
    await expect(laneRailItem(page, extras[0]!.id)).toHaveCount(0);
  });

  // -------------------------------------------------------------------------
  // Case 8 — Non-member POSTs find-or-create with X-Workspace-Slug=gamma →
  //          rejected. Note: the spec said 403; production middleware
  //          (RequireWorkspaceMember, MUL-132) returns 404 to hide
  //          existence. Asserting 404 is the live contract.
  // -------------------------------------------------------------------------
  test("case 8: find-or-create with X-Workspace-Slug=gamma is rejected for non-members", async ({
    page,
  }) => {
    // Pre-flight: confirm gamma is not visible to the user via the
    // membership-filtered mirrors endpoint — would catch a fixture leak
    // that left the user as a gamma member from a prior run.
    const mirrors = (await (await fetch(`${API_BASE}/api/global/chat/mirrors`, {
      headers: { Authorization: `Bearer ${token}` },
    })).json()) as { workspace_slug: string }[];
    expect(mirrors.some((m) => m.workspace_slug === V2_GAMMA_SLUG)).toBe(false);

    // Drive the call from the browser context so the spoofed header path
    // matches what a malicious frontend would actually do — we want the
    // backend lockdown asserted, not the api client's slug bookkeeping.
    const result = await page.evaluate(
      async ({ apiBase, jwt, slug }) => {
        const res = await fetch(`${apiBase}/api/chat/sessions/find-or-create`, {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            Authorization: `Bearer ${jwt}`,
            "X-Workspace-Slug": slug,
          },
          body: JSON.stringify({ agent_id: "00000000-0000-0000-0000-000000000000" }),
        });
        return { status: res.status, body: await res.text() };
      },
      { apiBase: API_BASE, jwt: token, slug: V2_GAMMA_SLUG },
    );

    // 404 is the existence-hiding contract. The session/agent IDs we sent
    // are nonsense; the membership reject happens in middleware before
    // any handler logic, so we never reach a 400/404-from-handler path.
    expect(result.status).toBe(404);
    expect(result.status).not.toBe(200);
    expect(result.status).not.toBe(201);
  });
});

// ---------------------------------------------------------------------------
// Locators / helpers — local to this spec because they only make sense
// inside the V2 lanes layout. The lane-rail / lane-workspace test ids are
// established in `lane-rail.tsx` and `global-chat-view.tsx`.
// ---------------------------------------------------------------------------

function laneRailItem(page: Page, laneId: string) {
  return page.locator(`[data-testid="lane-rail-item"][data-lane-id="${laneId}"]`);
}

function workspaceLane(page: Page, workspaceId: string) {
  return page.locator(
    `[data-testid="lane-workspace"][data-workspace-id="${workspaceId}"]`,
  );
}

function workspaceLaneRailItems(page: Page) {
  // The Global lane has data-lane-id="global" and is always present; this
  // selector returns only the workspace lanes that count against the cap.
  return page.locator(
    `[data-testid="lane-rail-item"]:not([data-lane-id="global"])`,
  );
}

function activeLaneAgentPicker(page: Page, workspaceId: string) {
  // The trigger renders both an avatar and the agent name; we scope to the
  // text span so the assertion is robust to avatar markup churn.
  return workspaceLane(page, workspaceId)
    .locator('[data-testid="agent-dropdown-trigger"] span')
    .first();
}

async function openLaneByTile(page: Page, workspaceSlug: string) {
  const tile = page.locator(
    `[data-testid="workspace-tile"][data-workspace-slug="${workspaceSlug}"]`,
  );
  await expect(tile).toBeVisible({ timeout: 10000 });
  await tile.click();
}

interface ChatSession {
  id: string;
}

async function findOrCreateAlphaSession(
  jwt: string,
  agentId: string,
): Promise<ChatSession> {
  const res = await fetch(`${API_BASE}/api/chat/sessions/find-or-create`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${jwt}`,
      "X-Workspace-Slug": "e2e-v2-alpha",
    },
    body: JSON.stringify({ agent_id: agentId }),
  });
  if (!res.ok) {
    throw new Error(`find-or-create failed: ${res.status}`);
  }
  return (await res.json()) as ChatSession;
}

async function postChatMessage(
  jwt: string,
  workspaceSlug: string,
  sessionId: string,
  body: string,
) {
  const res = await fetch(
    `${API_BASE}/api/chat/sessions/${sessionId}/messages`,
    {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Authorization: `Bearer ${jwt}`,
        "X-Workspace-Slug": workspaceSlug,
      },
      body: JSON.stringify({ content: body }),
    },
  );
  if (!res.ok) {
    throw new Error(`POST chat message failed: ${res.status}`);
  }
}

interface ChatMessageRow {
  id: string;
  content: string | null;
  role: string;
}

async function listChatMessages(
  jwt: string,
  workspaceSlug: string,
  sessionId: string,
): Promise<ChatMessageRow[]> {
  const res = await fetch(
    `${API_BASE}/api/chat/sessions/${sessionId}/messages`,
    {
      headers: {
        Authorization: `Bearer ${jwt}`,
        "X-Workspace-Slug": workspaceSlug,
      },
    },
  );
  if (!res.ok) {
    throw new Error(`GET chat messages failed: ${res.status}`);
  }
  return (await res.json()) as ChatMessageRow[];
}
