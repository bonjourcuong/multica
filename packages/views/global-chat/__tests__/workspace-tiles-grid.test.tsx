import { describe, it, expect } from "vitest";
import { render, fireEvent } from "@testing-library/react";
import { WorkspaceTilesGrid } from "../workspace-tiles-grid";
import type { WorkspaceTileSpec } from "../workspace-tile";

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
});
