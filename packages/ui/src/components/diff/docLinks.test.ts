import { describe, it, expect } from "vitest";
import { resolveDocPath, crossDocTarget } from "./docLinks";

describe("resolveDocPath", () => {
  it("resolves a bare filename to the current doc's directory", () => {
    expect(resolveDocPath("docs/design.md", "notes.md")).toBe("docs/notes.md");
  });
  it("keeps a bare filename at the root for a root doc", () => {
    expect(resolveDocPath("README.md", "notes.md")).toBe("notes.md");
  });
  it("walks up with ../", () => {
    expect(resolveDocPath("docs/design.md", "../README.md")).toBe("README.md");
    expect(resolveDocPath("docs/sub/a.md", "../../top.md")).toBe("top.md");
  });
  it("descends into subdirectories", () => {
    expect(resolveDocPath("docs/design.md", "sub/x.md")).toBe("docs/sub/x.md");
  });
  it("treats ./ as a no-op prefix", () => {
    expect(resolveDocPath("docs/design.md", "./notes.md")).toBe("docs/notes.md");
  });
});

describe("crossDocTarget", () => {
  it("resolves a bare same-directory markdown link", () => {
    expect(crossDocTarget("docs/a.md", "b.md")).toBe("docs/b.md");
  });
  it("strips a #fragment and resolves the doc path", () => {
    expect(crossDocTarget("docs/a.md", "b.md#section")).toBe("docs/b.md");
  });
  it("accepts .mdx and .markdown", () => {
    expect(crossDocTarget("a.md", "b.mdx")).toBe("b.mdx");
    expect(crossDocTarget("docs/a.md", "c.markdown")).toBe("docs/c.markdown");
  });
  it("ignores anchor-only links", () => {
    expect(crossDocTarget("a.md", "#section")).toBeNull();
  });
  it("ignores external links", () => {
    expect(crossDocTarget("a.md", "https://example.com/x.md")).toBeNull();
    expect(crossDocTarget("a.md", "mailto:x@example.com")).toBeNull();
  });
  it("ignores absolute paths", () => {
    expect(crossDocTarget("a.md", "/etc/x.md")).toBeNull();
  });
  it("ignores non-markdown targets", () => {
    expect(crossDocTarget("a.md", "script.js")).toBeNull();
    expect(crossDocTarget("a.md", "image.png")).toBeNull();
  });
});
