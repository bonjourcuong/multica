import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { WorkspaceBadge } from "./workspace-badge";

describe("WorkspaceBadge", () => {
  it("renders the prefix as the visible label", () => {
    render(<WorkspaceBadge color="#7c3aed" prefix="MUL" name="Multica Fork" />);
    const badge = screen.getByTestId("workspace-badge");
    expect(badge).toHaveTextContent("MUL");
  });

  it("paints the background with the server-derived color", () => {
    render(<WorkspaceBadge color="#22c55e" prefix="ACM" />);
    const badge = screen.getByTestId("workspace-badge");
    // jsdom normalizes inline-style colors to rgb(); accept either form so
    // the test does not depend on the renderer's serialization.
    const style = badge.getAttribute("style") ?? "";
    expect(
      style.includes("#22c55e") || style.includes("rgb(34, 197, 94)"),
    ).toBe(true);
  });

  it("uses '<name> (<prefix>)' as the a11y label when name is provided", () => {
    render(<WorkspaceBadge color="#000" prefix="ACM" name="Acme" />);
    expect(
      screen.getByLabelText("Acme (ACM)"),
    ).toBeInTheDocument();
  });

  it("falls back to the prefix as the a11y label when name is omitted", () => {
    render(<WorkspaceBadge color="#000" prefix="STD" />);
    expect(screen.getByLabelText("STD")).toBeInTheDocument();
  });
});
