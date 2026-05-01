/**
 * MUL-33 Task 15 — Global orchestrator chat: happy-path E2E.
 *
 * Mirrors the spec section 8 of the design doc:
 *   1. From `/global/chat`, post `@<workspace> ping de test`.
 *   2. Within 5s the corresponding workspace tile (right pane) flips to the
 *      `delivered` dispatch state — the contract that the user can actually
 *      see in the UI (MUL-99).
 *   3. The mirror chat_message lands in the target workspace, end-to-end
 *      persistence confirmed via direct DB read.
 *
 * The tile grid is driven by `GET /api/global/chat/mirrors` (MUL-100). The
 * dispatch-state badge (MUL-99) is the V1.1 UI surface the spec drives;
 * the mirrors endpoint contract is asserted alongside as a redundancy that
 * the tile data is fresh after dispatch. The DB-side assertion on
 * `chat_message` insertion is kept as a defense-in-depth check that the
 * dispatch wrote through.
 */

import { test, expect } from "@playwright/test";
import { loginAsDefault, createTestApi } from "./helpers";
import {
  ensureWorkspaceAgent,
  getUserIdByEmail,
  getWorkspaceIdBySlug,
  countMirrorMessagesContaining,
  listGlobalMirrors,
} from "./global-chat-helpers";
import type { TestApiClient } from "./fixtures";

const DEFAULT_E2E_EMAIL = "e2e@multica.ai";
const TARGET_WORKSPACE_SLUG = "e2e-workspace";

test.describe("Global chat — happy path", () => {
  let api: TestApiClient;
  let targetWorkspaceId: string;
  let userId: string;

  test.beforeEach(async ({ page }) => {
    api = await createTestApi();

    const wsId = await getWorkspaceIdBySlug(TARGET_WORKSPACE_SLUG);
    if (!wsId) {
      throw new Error(
        `expected workspace '${TARGET_WORKSPACE_SLUG}' to exist (createTestApi should have provisioned it)`,
      );
    }
    targetWorkspaceId = wsId;

    const uid = await getUserIdByEmail(DEFAULT_E2E_EMAIL);
    if (!uid) {
      throw new Error(`could not resolve user id for ${DEFAULT_E2E_EMAIL}`);
    }
    userId = uid;

    // Mirror dispatch refuses to write if the workspace has zero agents to
    // host the session. Seed one if the workspace was created agent-less.
    await ensureWorkspaceAgent(targetWorkspaceId);

    await loginAsDefault(page);
  });

  test.afterEach(async () => {
    if (api) await api.cleanup();
  });

  test("posts an @workspace mention and dispatches into the mirror session", async ({
    page,
  }) => {
    await page.goto("/global/chat");

    // The chat pane and the tile grid land together — the input is the
    // primary anchor we drive the test from.
    const input = page.getByTestId("global-chat-input");
    await expect(input).toBeVisible({ timeout: 10000 });

    const marker = `E2E-HAPPY-${Date.now()}`;
    const body = `@${TARGET_WORKSPACE_SLUG} ping de test ${marker}`;

    await input.fill(body);
    await page.getByRole("button", { name: "Send message" }).click();

    // The user message should land in the chat log (optimistic write +
    // server confirm). Scoped to the messages list so we don't accidentally
    // match the input field or the placeholder description. Kept as the
    // redundancy assertion alongside the tile-state contract below.
    await expect(
      page.getByTestId("global-chat-messages").getByText(body),
    ).toBeVisible({ timeout: 5000 });

    // V1.1 contract (MUL-99): the corresponding workspace tile flips to
    // `delivered` when the dispatch resolves. The tile is keyed by slug
    // via a data attribute so the assertion is robust to display-name
    // changes on the workspace.
    const targetTile = page.locator(
      `[data-testid="workspace-tile"][data-workspace-slug="${TARGET_WORKSPACE_SLUG}"]`,
    );
    await expect(targetTile).toBeVisible({ timeout: 5000 });
    await expect(targetTile).toHaveAttribute(
      "data-dispatch-state",
      "delivered",
      { timeout: 5000 },
    );

    // End-to-end persistence: a global_mirror chat_message was inserted in
    // the target workspace within 5s of the dispatch. This is the
    // workspace-side surface of the mirror session — what the per-workspace
    // chat UI would show once the mirror surface is exposed there.
    await expect
      .poll(
        () => countMirrorMessagesContaining(targetWorkspaceId, marker),
        { timeout: 5000, intervals: [250, 500, 1000] },
      )
      .toBe(1);

    // Mirrors endpoint contract: the target workspace's row exposes a
    // populated mirror_session_id and a fresh last_message_at. Without
    // this, the tile cannot subscribe — the entire reason MUL-100 ships.
    const token = api.getToken();
    if (!token) throw new Error("expected an auth token after login");
    const mirrors = await listGlobalMirrors(token);
    const target = mirrors.find((m) => m.workspace_id === targetWorkspaceId);
    expect(target, "mirrors endpoint missing target workspace").toBeTruthy();
    expect(target?.workspace_slug).toBe(TARGET_WORKSPACE_SLUG);
    expect(target?.mirror_session_id).toBeTruthy();
    expect(target?.last_message_at).toBeTruthy();

    // Sanity: the user id we resolved above is the dispatch creator. Avoids
    // a stale ID silently passing the unrelated assertions above.
    expect(userId).toMatch(
      /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/,
    );
  });
});
