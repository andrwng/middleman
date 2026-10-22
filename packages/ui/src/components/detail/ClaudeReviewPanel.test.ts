import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render } from "@testing-library/svelte";

const state = {
  run: null as null | Record<string, unknown>,
  active: false,
  open: false,
};
const cancel = vi.fn(async () => {});
const startRun = vi.fn(async () => true);
const togglePanel = vi.fn(() => {
  state.open = !state.open;
});
const closePanel = vi.fn(() => {
  state.open = false;
});

vi.mock("../../context.js", () => ({
  getStores: () => ({
    aiReviewRun: {
      getRun: () => state.run,
      isActive: () => state.active,
      isStarting: () => false,
      getError: () => null,
      isPanelOpen: () => state.open,
      togglePanel,
      closePanel,
      cancel,
      startRun,
    },
  }),
}));

import ClaudeReviewPanel from "./ClaudeReviewPanel.svelte";

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  state.run = null;
  state.active = false;
  state.open = false;
});

function aRun(extra: Record<string, unknown> = {}) {
  return { id: 1, status: "done", head_sha: "h", instructions: "focus on errors", summary: "Two findings.", raw_json: "", suggestion_count: 2, created_at: "2026-01-01T00:00:00Z", ...extra };
}

describe("ClaudeReviewPanel", () => {
  it("shows nothing until there is a run", () => {
    const { container } = render(ClaudeReviewPanel);
    expect(container.textContent?.trim()).toBe("");
  });

  it("the toggle reports progress and opens the panel", async () => {
    state.run = aRun({ status: "running", summary: "" });
    state.active = true;
    const { getByRole } = render(ClaudeReviewPanel);
    const toggle = getByRole("button", { name: /Reviewing… · 2 suggestions/ });
    await fireEvent.click(toggle);
    expect(togglePanel).toHaveBeenCalled();
  });

  it("shows the summary and instructions of a finished run", () => {
    state.run = aRun();
    state.open = true;
    const { getByText, getByRole } = render(ClaudeReviewPanel);
    expect(getByRole("dialog", { name: "Claude review" })).toBeTruthy();
    expect(getByText("Two findings.")).toBeTruthy();
    expect(getByText(/focus on errors/)).toBeTruthy();
  });

  it("shows the error and partial count of a failed run", () => {
    state.run = aRun({ status: "failed", error: "claude exited: 1", summary: "" });
    state.open = true;
    const { getByText } = render(ClaudeReviewPanel);
    expect(getByText(/claude exited: 1/)).toBeTruthy();
    expect(getByText(/2 suggestions were kept/)).toBeTruthy();
  });

  it("re-runs with the previous instructions prefilled", async () => {
    state.run = aRun();
    state.open = true;
    const { getByText, getByRole } = render(ClaudeReviewPanel);
    await fireEvent.click(getByText("Re-run"));
    expect((getByRole("textbox") as HTMLTextAreaElement).value).toBe("focus on errors");
  });

  it("offers Stop while the run is active", async () => {
    state.run = aRun({ status: "running", summary: "" });
    state.active = true;
    state.open = true;
    const { getByText } = render(ClaudeReviewPanel);
    await fireEvent.click(getByText("Stop"));
    expect(cancel).toHaveBeenCalled();
  });

  it("closes with Esc and with its close button", async () => {
    state.run = aRun();
    state.open = true;
    const { getByRole } = render(ClaudeReviewPanel);
    await fireEvent.keyDown(getByRole("dialog"), { key: "Escape" });
    expect(closePanel).toHaveBeenCalledTimes(1);
    await fireEvent.click(getByRole("button", { name: "Close Claude review" }));
    expect(closePanel).toHaveBeenCalledTimes(2);
  });

  it("renders the summary as markdown", () => {
    state.run = aRun({ summary: "Found **two** issues." });
    state.open = true;
    const { container } = render(ClaudeReviewPanel, { props: { owner: "acme", name: "widget" } });
    expect(container.querySelector(".claude-panel__summary strong")?.textContent).toBe("two");
  });
});
