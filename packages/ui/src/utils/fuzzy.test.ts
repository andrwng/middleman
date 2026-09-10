import { describe, it, expect } from "vitest";
import { fuzzyScore, fuzzyFilter } from "./fuzzy.js";

describe("fuzzyScore", () => {
  it("matches an empty query against anything with score 0", () => {
    expect(fuzzyScore("anything", "")).toBe(0);
  });

  it("matches a subsequence regardless of case", () => {
    expect(fuzzyScore("CodeBrowserPanel.svelte", "cbp")).not.toBeNull();
  });

  it("returns null when the query isn't a subsequence", () => {
    expect(fuzzyScore("handler.go", "xyz")).toBeNull();
  });

  it("scores a tighter match lower than a looser one", () => {
    const tight = fuzzyScore("handler.go", "hdl");
    const loose = fuzzyScore("has-a-different-loose-hit", "hdl");
    expect(tight).not.toBeNull();
    expect(loose).not.toBeNull();
    expect(tight!).toBeLessThan(loose!);
  });
});

describe("fuzzyFilter", () => {
  it("sorts alphabetically on an empty query", () => {
    expect(fuzzyFilter(["b.go", "a.go", "c.go"], "")).toEqual(["a.go", "b.go", "c.go"]);
  });

  it("drops non-matches and sorts by score", () => {
    const result = fuzzyFilter(["internal/handler.go", "README.md", "config.yaml"], "hdlr");
    expect(result).toEqual(["internal/handler.go"]);
  });

  it("breaks ties alphabetically", () => {
    // Same shape, same match distance for "ab" -- a genuine score tie.
    const result = fuzzyFilter(["2ab.go", "1ab.go"], "ab");
    expect(result).toEqual(["1ab.go", "2ab.go"]);
  });
});
