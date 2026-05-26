import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/svelte";
import { STORES_KEY } from "../../context.js";
import PatchsetPicker from "./PatchsetPicker.svelte";

function diffStub() {
  return {
    getPatchsets: () => [
      { id: 1, number: 1, head_sha: "a", base_sha: "x", merge_base_sha: "x", observed_at: "2026-05-01T00:00:00Z" },
      { id: 2, number: 2, head_sha: "b", base_sha: "a", merge_base_sha: "x", observed_at: "2026-05-02T00:00:00Z" },
    ],
    isPatchsetsLoading: () => false,
    getPatchsetsError: () => null,
    getScope: () => ({ kind: "head" }),
    loadPatchsets: vi.fn(async () => {}),
    resetToHead: vi.fn(),
    selectPatchsets: vi.fn(),
  };
}

function renderPicker(props: Record<string, unknown> = {}) {
  return render(PatchsetPicker, {
    props,
    context: new Map<symbol, unknown>([[STORES_KEY, { diff: diffStub() }]]),
  });
}

beforeEach(() => {
  localStorage.clear();
});

afterEach(() => {
  cleanup();
});

describe("PatchsetPicker collapse", () => {
  it("renders chips by default (not collapsed)", () => {
    const { container } = renderPicker();
    expect(container.querySelector(".ps-picker__chips")).toBeTruthy();
  });

  it("clicking the chevron toggles collapsed state and persists", async () => {
    renderPicker();
    const chevron = screen.getByLabelText(/collapse patchsets|expand patchsets/i);
    await fireEvent.click(chevron);
    expect(localStorage.getItem("pr-patchset-collapsed")).toBe("true");
  });

  it("renders without chips when pr-patchset-collapsed=true", () => {
    localStorage.setItem("pr-patchset-collapsed", "true");
    const { container } = renderPicker();
    expect(container.querySelector(".ps-picker__chips")).toBeNull();
  });

  it("chevron shows expanded state when forceExpanded=true even if pr-patchset-collapsed=true", () => {
    // Peeking a previously collapsed picker must flip the chevron and
    // its aria-label so it matches the visible chip strip.
    localStorage.setItem("pr-patchset-collapsed", "true");
    const { container } = renderPicker({ forceExpanded: true });
    expect(container.querySelector(".ps-picker__chevron--collapsed")).toBeNull();
    expect(screen.getByLabelText(/collapse patchsets/i)).toBeTruthy();
  });
});
