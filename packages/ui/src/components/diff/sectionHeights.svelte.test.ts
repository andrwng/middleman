import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  SECTION_MIN_HEIGHT,
  SECTION_RESERVE_BELOW,
  clampSectionHeight,
  clearSectionHeight,
  getSectionHeight,
  persistSectionHeight,
  setSectionHeight,
} from "./sectionHeights.svelte.js";

const KEY = "pr-section-height:commits";

// A tall sidebar leaves plenty of room: max = 900 - 120 = 780.
const TALL = 900;

describe("clampSectionHeight", () => {
  it("passes a mid-range value through, rounded to whole pixels", () => {
    expect(clampSectionHeight(300.4, TALL)).toBe(300);
    expect(clampSectionHeight(300.6, TALL)).toBe(301);
  });

  it("floors at SECTION_MIN_HEIGHT", () => {
    expect(clampSectionHeight(10, TALL)).toBe(SECTION_MIN_HEIGHT);
    expect(clampSectionHeight(-500, TALL)).toBe(SECTION_MIN_HEIGHT);
    expect(clampSectionHeight(0, TALL)).toBe(SECTION_MIN_HEIGHT);
  });

  it("caps so the panes below keep SECTION_RESERVE_BELOW", () => {
    expect(clampSectionHeight(5000, TALL)).toBe(TALL - SECTION_RESERVE_BELOW);
  });

  it("lets the floor win when the sidebar cannot fit both minimums", () => {
    // 150px sidebar: the cap would be 30, below the floor, so the floor wins
    // and the section overflows rather than becoming unusably short.
    expect(clampSectionHeight(400, 150)).toBe(SECTION_MIN_HEIGHT);
  });

  it("returns the floor for a non-finite desired height", () => {
    expect(clampSectionHeight(NaN, TALL)).toBe(SECTION_MIN_HEIGHT);
    expect(clampSectionHeight(Infinity, TALL)).toBe(TALL - SECTION_RESERVE_BELOW);
  });

  it("returns the floor when the sidebar height is unmeasurable", () => {
    expect(clampSectionHeight(400, NaN)).toBe(SECTION_MIN_HEIGHT);
    expect(clampSectionHeight(400, 0)).toBe(SECTION_MIN_HEIGHT);
  });
});

describe("section height state", () => {
  beforeEach(() => {
    localStorage.clear();
    clearSectionHeight("commits");
  });
  afterEach(() => {
    vi.restoreAllMocks();
    localStorage.clear();
  });

  it("reports null for a section the reader has never sized", () => {
    expect(getSectionHeight("commits")).toBeNull();
  });

  it("set updates the live value but does not touch storage", () => {
    setSectionHeight("commits", 240);
    expect(getSectionHeight("commits")).toBe(240);
    // Drags fire a move per pixel; only pointerup persists.
    expect(localStorage.getItem(KEY)).toBeNull();
  });

  it("persist writes the live value to storage", () => {
    setSectionHeight("commits", 240);
    persistSectionHeight("commits");
    expect(localStorage.getItem(KEY)).toBe("240");
  });

  it("persist is a no-op for an unsized section", () => {
    persistSectionHeight("commits");
    expect(localStorage.getItem(KEY)).toBeNull();
  });

  it("clear drops both the live value and the stored one", () => {
    setSectionHeight("commits", 240);
    persistSectionHeight("commits");
    clearSectionHeight("commits");
    expect(getSectionHeight("commits")).toBeNull();
    expect(localStorage.getItem(KEY)).toBeNull();
  });

  it("keeps the live value when storage rejects the write", () => {
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("QuotaExceededError");
    });
    setSectionHeight("commits", 240);
    expect(() => persistSectionHeight("commits")).not.toThrow();
    expect(getSectionHeight("commits")).toBe(240);
  });

  it("keeps sections independent", () => {
    setSectionHeight("commits", 240);
    setSectionHeight("threads", 310);
    expect(getSectionHeight("commits")).toBe(240);
    expect(getSectionHeight("threads")).toBe(310);
    clearSectionHeight("threads");
    expect(getSectionHeight("commits")).toBe(240);
    expect(getSectionHeight("threads")).toBeNull();
  });
});

describe("hydration from storage at module load", () => {
  afterEach(() => {
    localStorage.clear();
    vi.resetModules();
  });

  async function freshModule() {
    vi.resetModules();
    return await import("./sectionHeights.svelte.js");
  }

  it("adopts a stored height written by an earlier session", async () => {
    localStorage.setItem("pr-section-height:threads", "275");
    const mod = await freshModule();
    expect(mod.getSectionHeight("threads")).toBe(275);
  });

  it("ignores a stored value that is not a number", async () => {
    localStorage.setItem("pr-section-height:threads", "tall");
    const mod = await freshModule();
    expect(mod.getSectionHeight("threads")).toBeNull();
  });

  it("ignores a stored value below the floor", async () => {
    localStorage.setItem("pr-section-height:threads", "12");
    const mod = await freshModule();
    expect(mod.getSectionHeight("threads")).toBeNull();
  });
});
