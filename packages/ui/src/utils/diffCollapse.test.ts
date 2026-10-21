import { describe, expect, it } from "vitest";
import {
  LARGE_FILE_LINES,
  LAZY_DIFF_LINES,
  compileGlob,
  matchesAnyGlob,
  resolveCollapse,
  type CollapseInput,
} from "./diffCollapse.js";

function file(overrides: Partial<CollapseInput> = {}): CollapseInput {
  return {
    path: "a.go",
    status: "modified",
    additions: 1,
    deletions: 1,
    ...overrides,
  };
}

describe("compileGlob", () => {
  it("matches ** against any number of segments, including zero", () => {
    const re = compileGlob("proto/gen/**");
    expect(re.test("proto/gen/a/b.go")).toBe(true);
    expect(re.test("proto/gen/x.go")).toBe(true);
    expect(re.test("proto/generated/x.go")).toBe(false);
  });

  it("matches **/ prefix against zero or more leading segments", () => {
    const re = compileGlob("**/*.pb.go");
    expect(re.test("a.pb.go")).toBe(true);
    expect(re.test("x/y/a.pb.go")).toBe(true);
  });

  it("matches * only within a single path segment", () => {
    const re = compileGlob("*.lock");
    expect(re.test("yarn.lock")).toBe(true);
    expect(re.test("a/yarn.lock")).toBe(false);
  });

  it("matches ? as exactly one non-slash character", () => {
    const re = compileGlob("a?.go");
    expect(re.test("ab.go")).toBe(true);
    expect(re.test("a.go")).toBe(false);
    expect(re.test("abc.go")).toBe(false);
    expect(re.test("a/.go")).toBe(false);
  });

  it("treats regex metacharacters literally", () => {
    const re = compileGlob("a+b.go");
    expect(re.test("a+b.go")).toBe(true);
    expect(re.test("aab.go")).toBe(false);
    expect(re.test("aaab.go")).toBe(false);
  });

  it("anchors the match to the full path", () => {
    const re = compileGlob("*.lock");
    expect(re.test("yarn.lock.bak")).toBe(false);
    expect(re.test("xyarn.lock")).toBe(true);
  });

  describe("interior ** keeps exactly one separator between literals", () => {
    const re = compileGlob("a/**/b");
    it.each([
      ["a/b", true],
      ["a/x/b", true],
      ["a/x/y/b", true],
      ["ab", false],
      ["a/b/c", false],
      ["xa/b", false],
    ] as const)("a/**/b against %s -> %s", (path, expected) => {
      expect(re.test(path)).toBe(expected);
    });
  });

  describe("multiple interior ** segments each keep one separator", () => {
    const re = compileGlob("a/**/b/**/c");
    it.each([
      ["a/b/c", true],
      ["a/x/b/y/c", true],
      ["abc", false],
    ] as const)("a/**/b/**/c against %s -> %s", (path, expected) => {
      expect(re.test(path)).toBe(expected);
    });
  });

  describe("trailing ** requires the preceding separator and does not match the bare prefix", () => {
    const re = compileGlob("x/**");
    it.each([
      ["x/y", true],
      ["x/y/z", true],
      ["x", false],
    ] as const)("x/** against %s -> %s", (path, expected) => {
      expect(re.test(path)).toBe(expected);
    });
  });
});

describe("compileGlob pathological patterns", () => {
  // 30 segments, none named "zz"/ending in "x": every pattern below fails,
  // which forces a backtracking regex to explore its full search space.
  const path = Array.from({ length: 30 }, (_, i) => `seg${i}`).join("/");

  it.each([
    ["**/".repeat(20) + "zz", false],
    ["*".repeat(30) + "x", false],
    ["*/".repeat(15) + "zz", false],
    ["*/**/".repeat(10) + "zz", false],
    ["**".repeat(15) + "zz", false],
    ["**/seg".repeat(10) + "zz", false],
    ["**/".repeat(20) + "seg29", true],
    ["*".repeat(30) + "9", true],
  ] as const)("%s completes quickly -> %s", (glob, expected) => {
    const start = performance.now();
    const result = compileGlob(glob).test(path);
    const elapsed = performance.now() - start;
    expect(result).toBe(expected);
    expect(elapsed).toBeLessThan(50);
  });

  it("collapses consecutive ** segments without changing semantics", () => {
    const re = compileGlob("a/**/**/b");
    expect(re.test("a/b")).toBe(true);
    expect(re.test("a/x/y/b")).toBe(true);
    expect(re.test("ab")).toBe(false);
  });

  it("keeps mid-segment ** crossing separators", () => {
    const re = compileGlob("a**.go");
    expect(re.test("a.go")).toBe(true);
    expect(re.test("ab/c/d.go")).toBe(true);
    expect(re.test("b.go")).toBe(false);
  });
});

describe("matchesAnyGlob", () => {
  it("matches against the new path", () => {
    const globs = [compileGlob("proto/gen/**")];
    expect(matchesAnyGlob(globs, "proto/gen/x.go")).toBe(true);
  });

  it("matches against the old path for renames", () => {
    const globs = [compileGlob("proto/gen/**")];
    expect(matchesAnyGlob(globs, "new/path.go", "proto/gen/old.go")).toBe(
      true,
    );
  });

  it("returns false when neither path matches any glob", () => {
    const globs = [compileGlob("proto/gen/**")];
    expect(matchesAnyGlob(globs, "src/a.go", "src/old.go")).toBe(false);
  });

  it("returns false for an empty glob list", () => {
    expect(matchesAnyGlob([], "proto/gen/x.go")).toBe(false);
  });
});

describe("resolveCollapse", () => {
  it("expands a small, non-deleted file with no override and no glob match", () => {
    const result = resolveCollapse(file(), undefined, []);
    expect(result).toEqual({ collapsed: false, reason: null });
  });

  it("collapses with reason user when override is true, even for a small file", () => {
    const result = resolveCollapse(file(), true, []);
    expect(result).toEqual({ collapsed: true, reason: "user" });
  });

  it("expands with reason null when override is false, even if a glob matches", () => {
    const globs = [compileGlob("*.go")];
    const result = resolveCollapse(file({ path: "a.go" }), false, globs);
    expect(result).toEqual({ collapsed: false, reason: null });
  });

  it("collapses with reason pattern when a glob matches and there is no override", () => {
    const globs = [compileGlob("proto/gen/**")];
    const result = resolveCollapse(
      file({ path: "proto/gen/x.go" }),
      undefined,
      globs,
    );
    expect(result).toEqual({ collapsed: true, reason: "pattern" });
  });

  it("collapses with reason deleted for a whole-file deletion", () => {
    const result = resolveCollapse(
      file({ status: "deleted", additions: 0, deletions: 5 }),
      undefined,
      [],
    );
    expect(result).toEqual({ collapsed: true, reason: "deleted" });
  });

  it("collapses with reason large when additions+deletions exceed the threshold", () => {
    const result = resolveCollapse(
      file({ additions: LARGE_FILE_LINES, deletions: 1 }),
      undefined,
      [],
    );
    expect(result).toEqual({ collapsed: true, reason: "large" });
  });

  it("does not collapse for large when additions+deletions equal the threshold", () => {
    const result = resolveCollapse(
      file({ additions: LARGE_FILE_LINES, deletions: 0 }),
      undefined,
      [],
    );
    expect(result).toEqual({ collapsed: false, reason: null });
  });

  it("prefers pattern over large when both apply", () => {
    const globs = [compileGlob("proto/gen/**")];
    const result = resolveCollapse(
      file({
        path: "proto/gen/x.go",
        additions: LARGE_FILE_LINES + 100,
        deletions: 0,
      }),
      undefined,
      globs,
    );
    expect(result).toEqual({ collapsed: true, reason: "pattern" });
  });

  it("prefers deleted over large when both apply", () => {
    const result = resolveCollapse(
      file({
        status: "deleted",
        additions: 0,
        deletions: LARGE_FILE_LINES + 100,
      }),
      undefined,
      [],
    );
    expect(result).toEqual({ collapsed: true, reason: "deleted" });
  });

  it("prefers pattern over deleted when both apply", () => {
    const globs = [compileGlob("proto/gen/**")];
    const result = resolveCollapse(
      file({ path: "proto/gen/x.go", status: "deleted", deletions: 5 }),
      undefined,
      globs,
    );
    expect(result).toEqual({ collapsed: true, reason: "pattern" });
  });

  it("matches the glob against old_path for a rename", () => {
    const globs = [compileGlob("proto/gen/**")];
    const result = resolveCollapse(
      file({ path: "new/path.go", old_path: "proto/gen/old.go" }),
      undefined,
      globs,
    );
    expect(result).toEqual({ collapsed: true, reason: "pattern" });
  });

  it("exposes LAZY_DIFF_LINES as the module-level lazy threshold constant", () => {
    expect(LAZY_DIFF_LINES).toBe(5000);
  });

  it("exposes LARGE_FILE_LINES as the collapse threshold constant", () => {
    expect(LARGE_FILE_LINES).toBe(1500);
  });
});
