import { describe, it, expect, vi } from "vitest";
import { render } from "@testing-library/react";
import { WorkspaceTile, type WorkspaceTileSpec } from "../workspace-tile";

vi.mock("../use-workspace-mirror", () => ({
  useWorkspaceMirror: () => ({ messages: [] }),
}));

const SPEC: WorkspaceTileSpec = {
  workspace_id: "ws-1",
  workspace_slug: "ws-1",
  workspace_name: "Workspace 1",
  mirror_session_id: null,
  last_message_at: null,
};

describe("WorkspaceTile dispatch state badge", () => {
  it("renders no badge for the default 'idle' state", () => {
    const { queryByTestId } = render(<WorkspaceTile workspace={SPEC} />);
    expect(queryByTestId("tile-dispatch-state")).toBeNull();
  });

  it("renders a 'sending' badge while a dispatch is in flight", () => {
    const { getByTestId } = render(
      <WorkspaceTile workspace={SPEC} dispatchState="sending" />,
    );
    const badge = getByTestId("tile-dispatch-state");
    expect(badge.dataset.state).toBe("sending");
    expect(badge.textContent).toMatch(/sending/i);
  });

  it("renders a 'delivered' badge after a successful dispatch", () => {
    const { getByTestId } = render(
      <WorkspaceTile workspace={SPEC} dispatchState="delivered" />,
    );
    const badge = getByTestId("tile-dispatch-state");
    expect(badge.dataset.state).toBe("delivered");
    expect(badge.textContent).toMatch(/delivered/i);
  });

  it("renders a 'no access' badge when membership rejects the dispatch", () => {
    const { getByTestId } = render(
      <WorkspaceTile workspace={SPEC} dispatchState="not_authorized" />,
    );
    const badge = getByTestId("tile-dispatch-state");
    expect(badge.dataset.state).toBe("not_authorized");
    expect(badge.textContent).toMatch(/no access/i);
  });

  it("renders an 'error' badge for any other dispatch failure", () => {
    const { getByTestId } = render(
      <WorkspaceTile workspace={SPEC} dispatchState="error" />,
    );
    const badge = getByTestId("tile-dispatch-state");
    expect(badge.dataset.state).toBe("error");
    expect(badge.textContent).toMatch(/failed/i);
  });

  it("exposes the dispatch state on the tile root for E2E assertions", () => {
    const { getByTestId } = render(
      <WorkspaceTile workspace={SPEC} dispatchState="delivered" />,
    );
    const root = getByTestId("workspace-tile");
    expect(root.dataset.dispatchState).toBe("delivered");
    expect(root.dataset.workspaceSlug).toBe("ws-1");
  });
});
