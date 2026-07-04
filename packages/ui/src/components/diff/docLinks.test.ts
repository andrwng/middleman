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
  it("resolves a bare same-directory markdown link (no fragment)", () => {
    expect(crossDocTarget("docs/a.md", "b.md")).toEqual({ path: "docs/b.md", fragment: "" });
  });
  it("captures a #fragment alongside the resolved doc path", () => {
    expect(crossDocTarget("docs/a.md", "b.md#section")).toEqual({
      path: "docs/b.md",
      fragment: "section",
    });
    expect(crossDocTarget("docs/a.md", "../README.md#install")).toEqual({
      path: "README.md",
      fragment: "install",
    });
  });
  it("accepts .mdx and .markdown", () => {
    expect(crossDocTarget("a.md", "b.mdx")).toEqual({ path: "b.mdx", fragment: "" });
    expect(crossDocTarget("docs/a.md", "c.markdown")).toEqual({
      path: "docs/c.markdown",
      fragment: "",
    });
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
