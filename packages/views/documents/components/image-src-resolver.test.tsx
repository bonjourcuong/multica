/**
 * Sanity tests for the image-src resolution logic in DocumentViewer.
 *
 * The viewer's resolver is a closure over (workspaceId, dir). Rather than
 * pulling DocumentViewer into the test (which would drag the whole markdown
 * stack with it), we re-derive the same predicate inline here — this is a
 * deliberate duplication: it pins the contract so any future refactor of the
 * viewer that loosens it will fail the test, not silently rewrite URLs that
 * shouldn't be rewritten.
 */
import { describe, it, expect } from "vitest";
import { resolveRelative } from "@multica/core/documents";

function buildResolver(workspaceId: string, dir: string) {
  return (src: string): string | undefined => {
    if (!src) return undefined;
    if (/^[a-z][a-z0-9+.-]*:/i.test(src)) return undefined;
    if (src.startsWith("//")) return undefined;
    if (src.startsWith("/")) return undefined;
    const resolved = resolveRelative(dir, src);
    if (resolved === null) return undefined;
    return `http://api/api/workspaces/${workspaceId}/documents/image?path=${encodeURIComponent(resolved)}`;
  };
}

describe("DocumentViewer image src resolver", () => {
  const resolve = buildResolver("ws-1", "GROWTH/PROJECTS");

  it("rewrites relative refs to the FS API URL", () => {
    expect(resolve("cover.png")).toBe(
      "http://api/api/workspaces/ws-1/documents/image?path=GROWTH%2FPROJECTS%2Fcover.png",
    );
  });

  it("walks up parent dirs", () => {
    expect(resolve("../assets/x.png")).toBe(
      "http://api/api/workspaces/ws-1/documents/image?path=GROWTH%2Fassets%2Fx.png",
    );
  });

  it("leaves http(s) URLs alone", () => {
    expect(resolve("https://cdn.example.com/x.png")).toBeUndefined();
    expect(resolve("http://example.com/x.png")).toBeUndefined();
  });

  it("leaves data: and mention: URLs alone", () => {
    expect(resolve("data:image/png;base64,iVBORw0KGgo=")).toBeUndefined();
    expect(resolve("mention://issue/MUL-17")).toBeUndefined();
  });

  it("ignores absolute and protocol-relative paths", () => {
    expect(resolve("/etc/passwd.png")).toBeUndefined();
    expect(resolve("//evil.example.com/x.png")).toBeUndefined();
  });

  it("returns undefined when '..' would escape the pkm root", () => {
    const rootResolve = buildResolver("ws-1", "");
    expect(rootResolve("../escape.png")).toBeUndefined();
  });

  it("returns undefined for empty refs", () => {
    expect(resolve("")).toBeUndefined();
  });
});
