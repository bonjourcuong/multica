import { describe, it, expect } from "vitest";
import {
  parentPath,
  basename,
  joinPath,
  resolveRelative,
  breadcrumbs,
} from "./path-utils";

describe("parentPath", () => {
  it("returns the directory of a file path", () => {
    expect(parentPath("a/b/c.md")).toBe("a/b");
  });
  it("returns empty string for top-level entries", () => {
    expect(parentPath("README.md")).toBe("");
  });
  it("returns empty string for the root", () => {
    expect(parentPath("")).toBe("");
  });
  it("strips leading and trailing slashes", () => {
    expect(parentPath("/foo/bar/")).toBe("foo");
  });
});

describe("basename", () => {
  it("returns the last segment", () => {
    expect(basename("a/b/c.md")).toBe("c.md");
    expect(basename("README.md")).toBe("README.md");
  });
  it("handles trailing slashes", () => {
    expect(basename("a/b/")).toBe("b");
  });
  it("returns empty for empty input", () => {
    expect(basename("")).toBe("");
  });
});

describe("joinPath", () => {
  it("joins segments with slashes", () => {
    expect(joinPath("a", "b", "c.md")).toBe("a/b/c.md");
  });
  it("ignores empty segments", () => {
    expect(joinPath("", "a", "", "b")).toBe("a/b");
  });
  it("ignores '.' segments", () => {
    expect(joinPath("a", ".", "b")).toBe("a/b");
  });
});

describe("resolveRelative", () => {
  it("resolves a sibling reference against the file's parent dir", () => {
    expect(resolveRelative("posts", "image.png")).toBe("posts/image.png");
  });
  it("resolves './' references", () => {
    expect(resolveRelative("posts", "./img/cover.png")).toBe("posts/img/cover.png");
  });
  it("walks up with '..'", () => {
    expect(resolveRelative("posts/2024", "../assets/x.png")).toBe("posts/assets/x.png");
  });
  it("returns null when '..' would escape the root", () => {
    expect(resolveRelative("", "../foo.png")).toBeNull();
    expect(resolveRelative("a", "../../foo.png")).toBeNull();
  });
  it("returns null for empty refs", () => {
    expect(resolveRelative("a", "")).toBeNull();
  });
});

describe("breadcrumbs", () => {
  it("returns just root for empty path", () => {
    expect(breadcrumbs("")).toEqual([{ name: "", path: "" }]);
  });
  it("uses root label when provided", () => {
    expect(breadcrumbs("", "Documents")).toEqual([{ name: "Documents", path: "" }]);
  });
  it("builds cumulative paths", () => {
    expect(breadcrumbs("a/b/c.md")).toEqual([
      { name: "", path: "" },
      { name: "a", path: "a" },
      { name: "b", path: "a/b" },
      { name: "c.md", path: "a/b/c.md" },
    ]);
  });
});
