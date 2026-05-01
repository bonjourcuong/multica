import { describe, it, expect, vi } from "vitest";
import { render, fireEvent } from "@testing-library/react";
import { WorkspaceTilesGrid } from "../workspace-tiles-grid";
import type { WorkspaceTileSpec } from "../workspace-tile";

// Tiles open a realtime subscription via use-workspace-mirror. The grid is
// the unit under test here, so stub the hook out — the mirror behaviour is
// covered by its own tests, and we don't want to require a WSProvider just
// to assert grid layout.
vi.mock("../use-workspace-mirror", () => ({
  useWorkspaceMirror: () => ({ messages: [] }),
}));

function makeSpecs(n: number): WorkspaceTileSpec[] {
  return Array.from({ length: n }).map((_, i) => ({
    workspace_id: `ws-${i}`,
    workspace_slug: `slug-${i}`,
    workspace_name: `Workspace ${i}`,
    mirror_session_id: null,
    last_message_at: null,
  }));
}

describe("WorkspaceTilesGrid", () => {
  it("renders one tile per workspace when count is at or below the cap", () => {
    const specs = makeSpecs(5);
    const { getAllByTestId } = render(
      <WorkspaceTilesGrid workspaces={specs} />,
    );
    expect(getAllByTestId("workspace-tile")).toHaveLength(5);
  });

  it("wraps tiles in a single horizontal scroll container that contains a 2-row grid", () => {
    const { getByTestId } = render(
      <WorkspaceTilesGrid workspaces={makeSpecs(5)} />,
    );
    const scroll = getByTestId("tiles-scroll");
    const grid = getByTestId("tiles-grid");
    expect(scroll.contains(grid)).toBe(true);
    expect(scroll.className).toMatch(/overflow-x-auto/);
    // Inline style is the source of truth for the grid shape so the test
    // does not depend on Tailwind running in jsdom.
    expect(grid.style.gridTemplateRows).toMatch(/1fr 1fr/);
    expect(grid.style.gridAutoFlow).toBe("column");
  });

  it("caps rendered tiles to 12 by default (ADR D7)", () => {
    const specs = makeSpecs(15);
    const { getAllByTestId, getByTestId } = render(
      <WorkspaceTilesGrid workspaces={specs} />,
    );
    expect(getAllByTestId("workspace-tile")).toHaveLength(12);
    // Affordance for the rest is visible and reports the hidden count.
    const showMore = getByTestId("show-more-tiles");
    expect(showMore.textContent).toMatch(/3/);
  });

  it("reveals all tiles when 'show more' is clicked", () => {
    const specs = makeSpecs(15);
    const { getAllByTestId, getByTestId, queryByTestId } = render(
      <WorkspaceTilesGrid workspaces={specs} />,
    );
    fireEvent.click(getByTestId("show-more-tiles"));
    expect(getAllByTestId("workspace-tile")).toHaveLength(15);
    expect(queryByTestId("show-more-tiles")).toBeNull();
  });

  it("renders an empty state when there are no workspaces", () => {
    const { queryAllByTestId, getByTestId } = render(
      <WorkspaceTilesGrid workspaces={[]} />,
    );
    expect(queryAllByTestId("workspace-tile")).toHaveLength(0);
    expect(getByTestId("tiles-empty")).toBeInTheDocument();
  });

  it("forwards per-workspace dispatch state to each tile", () => {
    const specs = makeSpecs(3);
    const { getAllByTestId } = render(
      <WorkspaceTilesGrid
        workspaces={specs}
        tileStates={{
          "ws-0": "delivered",
          "ws-2": "not_authorized",
        }}
      />,
    );
    const tiles = getAllByTestId("workspace-tile");
    expect(tiles[0]?.dataset.dispatchState).toBe("delivered");
    // Tile without an entry stays idle (default).
    expect(tiles[1]?.dataset.dispatchState).toBe("idle");
    expect(tiles[2]?.dataset.dispatchState).toBe("not_authorized");
  });
});
