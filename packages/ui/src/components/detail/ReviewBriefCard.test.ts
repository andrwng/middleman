import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/svelte";
import { STORES_KEY } from "../../context.js";
import ReviewBriefCard from "./ReviewBriefCard.svelte";

// ReviewBriefCard reads three stores: `brief`, `diff`, and `fileResolver`.
// The test stubs cover only what the component actually calls when a brief
// is present (so the body markup renders and we can assert against it).
function briefStub() {
  return {
    current: () => ({
      pull_request_id: 1,
      head_sha: "abc1234deadbeef",
      depth: "quick" as const,
      status: "done" as const,
      content: "## Intent\n\nA reading-aid brief body.\n",
      error: null,
      created_at: "2026-05-01T00:00:00Z",
      updated_at: "2026-05-01T00:00:00Z",
    }),
    isLoading: () => false,
    isInFlight: () => false,
    isStale: () => false,
    getError: () => null,
    fetchLatest: vi.fn(async () => {}),
    generate: vi.fn(async () => {}),
    remove: vi.fn(async () => {}),
    start: vi.fn(),
    stop: vi.fn(),
  };
}

function diffStub() {
  return {
    getCommits: () => [
      { sha: "abc1234deadbeef", message: "msg", author_login: "alice", authored_at: "2026-05-01T00:00:00Z" },
    ],
  };
}

function fileResolverStub() {
  return {
    resolve: vi.fn(async () => {}),
    lookup: vi.fn(() => undefined),
    getVersion: vi.fn(() => 0),
  };
}

function renderCard() {
  return render(ReviewBriefCard, {
    props: { owner: "acme", name: "widget", number: 1 },
    context: new Map<symbol, unknown>([
      [STORES_KEY, { brief: briefStub(), diff: diffStub(), fileResolver: fileResolverStub() }],
    ]),
  });
}

beforeEach(() => {
  localStorage.clear();
});

afterEach(() => {
  cleanup();
});

describe("ReviewBriefCard collapse persistence", () => {
  it("clicking the chevron persists pr-brief-collapsed", async () => {
    renderCard();
    const toggle = screen.getByRole("button", { name: /brief/i });
    await fireEvent.click(toggle);
    expect(localStorage.getItem("pr-brief-collapsed")).not.toBeNull();
  });

  it("respects pr-brief-collapsed=true on first render", () => {
    localStorage.setItem("pr-brief-collapsed", "true");
    const { container } = renderCard();
    expect(container.querySelector(".brief__body")).toBeNull();
  });
});
