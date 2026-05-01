/**
 * MUL-7 - Cross-workspace meta view E2E coverage.
 *
 * Covers the shipped `/global` Kanban contract: rail entry, 5-column board,
 * workspace badges, card navigation, workspace filtering, URL persistence,
 * membership isolation, status filtering, and realtime fan-out across every
 * workspace the user is a member of.
 */

import { test, expect } from "@playwright/test";
import {
  META_STATUSES,
  createRealtimeIssue,
  listCrossWorkspaceIssues,
  loginLimitedMetaUser,
  loginSeededMetaUser,
  type MetaFixture,
} from "./cross-workspace-meta-view-helpers";

test.describe("Cross-workspace meta view", () => {
  let fixture: MetaFixture;

  test.beforeEach(async ({ page }) => {
    fixture = await loginSeededMetaUser(page);
  });

  test("shows all workspaces and renders the global Kanban", async ({ page }) => {
    await page.goto(`/${fixture.workspaces[0]!.slug}/issues`);

    await expect(
      page.getByRole("link", { name: "All workspaces" }),
    ).toBeVisible();
    for (const ws of fixture.workspaces) {
      await expect(page.getByRole("link", { name: ws.name })).toBeVisible();
    }

    await page.getByRole("link", { name: "All workspaces" }).click();
    await expect(page).toHaveURL(/\/global$/);
    await expect(page.getByTestId("global-kanban")).toBeVisible();

    for (const status of META_STATUSES) {
      await expect(
        page.getByTestId(`global-kanban-column-${status}`),
      ).toBeVisible();
    }

    await expect(page.getByTestId("global-kanban-card")).toHaveCount(30);
    await expect(page.getByTestId("workspace-badge")).toHaveCount(30);

    for (const ws of fixture.workspaces) {
      await expect(
        page.getByTestId("workspace-badge").filter({ hasText: ws.issuePrefix }),
      ).toHaveCount(10);
    }
  });

  test("navigates from a global card to the owning workspace issue detail", async ({
    page,
  }) => {
    const issue = fixture.issues[0]!;

    await page.goto("/global");
    await expect(page.getByTestId("global-kanban-card")).toHaveCount(30);
    await page.getByRole("link", { name: new RegExp(issue.title) }).click();

    await expect(page).toHaveURL(
      new RegExp(`/${issue.workspace.slug}/issues/${issue.id}`),
    );
    await expect(page.getByText(issue.title)).toBeVisible();
  });

  test("filters by selected workspaces and preserves the filter on refresh", async ({
    page,
  }) => {
    const [alpha, beta, gamma] = fixture.workspaces;

    await page.goto("/global");
    await expect(page.getByTestId("global-kanban-card")).toHaveCount(30);

    await page.getByTestId(`workspace-filter-chip-${alpha!.slug}`).click();
    await page.getByTestId(`workspace-filter-chip-${beta!.slug}`).click();

    await expect(page).toHaveURL(new RegExp(`workspace_ids=.*${alpha!.id}`));
    await expect(page).toHaveURL(new RegExp(`workspace_ids=.*${beta!.id}`));
    await expect(page.getByTestId("global-kanban-card")).toHaveCount(20);
    await expect(
      page.getByTestId("workspace-badge").filter({ hasText: alpha!.issuePrefix }),
    ).toHaveCount(10);
    await expect(
      page.getByTestId("workspace-badge").filter({ hasText: beta!.issuePrefix }),
    ).toHaveCount(10);
    await expect(
      page.getByTestId("workspace-badge").filter({ hasText: gamma!.issuePrefix }),
    ).toHaveCount(0);

    await page.reload();
    await expect(page.getByTestId("global-kanban-card")).toHaveCount(20);
    await expect(
      page.getByTestId("workspace-filter-chip-" + alpha!.slug),
    ).toHaveAttribute("aria-checked", "true");
    await expect(
      page.getByTestId("workspace-filter-chip-" + beta!.slug),
    ).toHaveAttribute("aria-checked", "true");
    await expect(
      page.getByTestId("workspace-filter-chip-" + gamma!.slug),
    ).toHaveAttribute("aria-checked", "false");
  });

  test("does not leak other users' workspaces or issues", async ({ page }) => {
    const limited = await loginLimitedMetaUser(page);

    await page.goto("/global");
    await expect(page.getByTestId("global-kanban-card")).toHaveCount(10);
    await expect(
      page.getByTestId("workspace-badge").filter({
        hasText: limited.workspaces[0]!.issuePrefix,
      }),
    ).toHaveCount(10);

    for (const ws of fixture.workspaces) {
      await expect(
        page.getByTestId("workspace-badge").filter({ hasText: ws.issuePrefix }),
      ).toHaveCount(0);
      await expect(page.getByRole("link", { name: ws.name })).toHaveCount(0);
    }

    const payload = await listCrossWorkspaceIssues(
      limited.token,
      `workspace_ids=${fixture.workspaces.map((ws) => ws.id).join(",")}`,
    );
    expect(payload.total_returned).toBe(0);
  });

  test("backend status filter returns only matching issues", async () => {
    const payload = await listCrossWorkspaceIssues(
      fixture.token,
      "status=in_progress",
    );
    expect(payload.total_returned).toBe(6);
    expect(payload.issues.every((issue) => issue.status === "in_progress")).toBe(
      true,
    );
  });

  test("UI status filter narrows the board and updates the URL", async ({
    page,
  }) => {
    await page.goto("/global");
    await expect(page.getByTestId("global-kanban-card")).toHaveCount(30);

    await page.getByTestId("status-filter-chip-in_progress").click();

    await expect(page).toHaveURL(/[?&]status=in_progress\b/);
    await expect(page.getByTestId("global-kanban-card")).toHaveCount(6);
    await expect(
      page.getByTestId("status-filter-chip-in_progress"),
    ).toHaveAttribute("aria-checked", "true");

    await page.reload();
    await expect(
      page.getByTestId("status-filter-chip-in_progress"),
    ).toHaveAttribute("aria-checked", "true");
    await expect(page.getByTestId("global-kanban-card")).toHaveCount(6);

    await page.getByTestId("status-filter-clear").click();
    await expect(page).not.toHaveURL(/[?&]status=/);
    await expect(page.getByTestId("global-kanban-card")).toHaveCount(30);
  });

  test("realtime issue creation appears on /global within 2s", async ({
    page,
  }) => {
    await page.goto("/global");
    await expect(page.getByTestId("global-kanban-card")).toHaveCount(30);

    const title = `E2E Meta realtime ${Date.now()}`;
    await createRealtimeIssue(fixture.workspaces[0]!, fixture.token, title);

    await expect(page.getByText(title)).toBeVisible({ timeout: 2000 });
    await expect(page.getByTestId("global-kanban-card")).toHaveCount(31);
  });
});
