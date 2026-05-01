import { describe, it, expect } from "vitest";
import {
  classifyDispatchTarget,
  parseMentionSlugs,
} from "./dispatch-state";
import type { GlobalDispatchTarget } from "../types";

function makeTarget(over: Partial<GlobalDispatchTarget> = {}): GlobalDispatchTarget {
  return {
    workspace_slug: "ws-1",
    workspace_id: "00000000-0000-0000-0000-000000000001",
    mirror_session_id: "00000000-0000-0000-0000-000000000010",
    mirror_message_id: "00000000-0000-0000-0000-000000000020",
    ...over,
  };
}

describe("classifyDispatchTarget", () => {
  it("classifies a successful dispatch as 'delivered'", () => {
    expect(classifyDispatchTarget(makeTarget())).toBe("delivered");
  });

  it("classifies the membership-rejection humanizer as 'not_authorized'", () => {
    expect(
      classifyDispatchTarget(
        makeTarget({
          workspace_id: "",
          mirror_session_id: "",
          mirror_message_id: "",
          error: "Je n'ai pas accès à `@stranger`.",
        }),
      ),
    ).toBe("not_authorized");
  });

  it("classifies any other non-empty error as 'error'", () => {
    expect(
      classifyDispatchTarget(
        makeTarget({
          workspace_id: "",
          mirror_session_id: "",
          mirror_message_id: "",
          error: "Workspace `@unknown` introuvable.",
        }),
      ),
    ).toBe("error");
  });

  it("treats an empty error string as 'delivered' (Go omitempty edge)", () => {
    expect(classifyDispatchTarget(makeTarget({ error: "" }))).toBe("delivered");
  });
});

describe("parseMentionSlugs", () => {
  it("returns the slug from a single @workspace mention", () => {
    expect(parseMentionSlugs("@fuchsia-b2b ping")).toEqual(["fuchsia-b2b"]);
  });

  it("returns slugs in order for multi-target dispatch", () => {
    expect(parseMentionSlugs("@ws1 @ws2 ping")).toEqual(["ws1", "ws2"]);
  });

  it("strips the agent suffix when present", () => {
    expect(parseMentionSlugs("@fuchsia-b2b:Tony help")).toEqual(["fuchsia-b2b"]);
  });

  it("ignores @ inside email addresses", () => {
    expect(parseMentionSlugs("contact foo@bar.com please")).toEqual([]);
  });

  it("returns [] when there are no mentions", () => {
    expect(parseMentionSlugs("hello world")).toEqual([]);
  });

  it("handles a mention at the very start of the string", () => {
    expect(parseMentionSlugs("@team-x kick off")).toEqual(["team-x"]);
  });
});
