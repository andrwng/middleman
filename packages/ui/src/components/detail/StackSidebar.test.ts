import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/svelte";
import { STORES_KEY, API_CLIENT_KEY, NAVIGATE_KEY } from "../../context.js";
import StackSidebar from "./StackSidebar.svelte";

function clientStub() {
  return {
    GET: vi.fn(async () => ({
      data: {
        in_stack: true,
        stack_id: 1,
        stack_name: "demo-stack",
        position: 1,
        size: 3,
        health: "ok",
        members: [
          { number: 1, title: "PR 1", state: "open", ci_status: "success",
            review_decision: "APPROVED", position: 1, is_draft: false,
            base_branch: "main", blocked_by: null },
        ],
      },
      error: undefined,
    })),
  };
}

function syncStub() {
  return { subscribeSyncComplete: () => () => {} };
}

function renderSidebar() {
  return render(StackSidebar, {
    props: { owner: "acme", name: "widget", number: 1 },
    context: new Map<symbol, unknown>([
      [STORES_KEY, { sync: syncStub() }],
      [API_CLIENT_KEY, clientStub()],
      [NAVIGATE_KEY, vi.fn()],
    ]),
  });
}

beforeEach(() => {
  localStorage.clear();
});

afterEach(() => {
  cleanup();
});

describe("StackSidebar collapse", () => {
  it("clicking the collapse button writes pr-stack-sidebar-collapsed", async () => {
    renderSidebar();
    // Allow fetchStack to resolve.
    await new Promise((r) => setTimeout(r, 0));
    const toggle = await screen.findByLabelText(/collapse stack|expand stack/i);
    await fireEvent.click(toggle);
    expect(localStorage.getItem("pr-stack-sidebar-collapsed")).toBe("true");
  });

  it("renders as a rail when pr-stack-sidebar-collapsed=true", async () => {
    localStorage.setItem("pr-stack-sidebar-collapsed", "true");
    const { container } = renderSidebar();
    await new Promise((r) => setTimeout(r, 0));
    expect(container.querySelector(".stack-sidebar--rail")).toBeTruthy();
  });
});
