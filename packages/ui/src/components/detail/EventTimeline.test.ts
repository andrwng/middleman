import { cleanup, render, screen, fireEvent } from "@testing-library/svelte";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import EventTimeline from "./EventTimeline.svelte";
import { STORES_KEY } from "../../context.js";
import type { PREvent } from "../../api/types.js";

function makeEvent(overrides: Partial<PREvent> = {}): PREvent {
  return {
    ID: 1,
    MergeRequestID: 42,
    PlatformID: null,
    EventType: "force_push",
    Author: "alice",
    Body: "",
    Summary: "aaaaaaa -> bbbbbbb",
    MetadataJSON: JSON.stringify({
      before_sha: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
      after_sha: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
    }),
    DedupeKey: "force-push-1",
    CreatedAt: "2024-06-01T12:00:00Z",
    ...overrides,
  } as PREvent;
}

function timelineDetailStub(opts: { hidden?: number[]; showing?: boolean } = {}) {
  const hidden = opts.hidden ?? [];
  return {
    getHiddenRootSet: () => new Set<number>(hidden),
    getHiddenThreadCount: () => hidden.length,
    isShowingHiddenThreads: () => opts.showing ?? false,
    setShowHiddenThreads: vi.fn(),
    getReviewCommentRootForPlatformID: (pid: number) => pid,
    hideReviewThread: vi.fn(async () => {}),
    unhideReviewThread: vi.fn(async () => {}),
  };
}

function renderTimeline(
  events: PREvent[],
  detail: ReturnType<typeof timelineDetailStub> = timelineDetailStub(),
) {
  return render(EventTimeline, {
    props: { events, repoOwner: "acme", repoName: "widget" },
    context: new Map<symbol, unknown>([[STORES_KEY, { detail }]]),
  });
}

describe("EventTimeline", () => {
  beforeEach(() => {
    // Force-push events are "mechanics" and hidden by default; opt in so this
    // test can see the rendered force-push card.
    localStorage.setItem("activity-show-mechanics", "true");
  });
  afterEach(() => {
    cleanup();
    localStorage.removeItem("activity-show-mechanics");
  });

  it("renders force-push label, actor, and SHA transition", () => {
    renderTimeline([makeEvent()]);

    const label = screen.getByText("Force-pushed");
    expect(label).toBeTruthy();
    expect(label.getAttribute("style")).toContain("var(--accent-red)");
    expect(screen.getByText("alice")).toBeTruthy();
    expect(screen.getByText("aaaaaaa -> bbbbbbb")).toBeTruthy();
  });
});

describe("EventTimeline hidden-thread filtering", () => {
  afterEach(() => {
    cleanup();
  });

  it("skips hidden review_comment events when showing is false", () => {
    const events: PREvent[] = [
      makeEvent({
        ID: 1, PlatformID: 500, EventType: "review_comment",
        Body: "hidden body",
        MetadataJSON: JSON.stringify({ path: "f.go", line: 1, side: "RIGHT" }),
        DedupeKey: "review-comment-500",
        Summary: "f.go",
      }),
      makeEvent({
        ID: 2, PlatformID: 600, EventType: "review_comment",
        Body: "visible body",
        MetadataJSON: JSON.stringify({ path: "g.go", line: 2, side: "RIGHT" }),
        DedupeKey: "review-comment-600",
        Summary: "g.go",
      }),
    ];
    const { container } = renderTimeline(events, timelineDetailStub({ hidden: [500] }));
    expect(container.textContent).not.toContain("hidden body");
    expect(container.textContent).toContain("visible body");
  });

  it("does not render Show Hidden toggle when there are zero hidden threads", () => {
    const { container } = renderTimeline([]);
    expect(container.querySelector(".hidden-toggle")).toBeNull();
  });

  it("renders Show Hidden (N) toggle when there is a hidden thread", () => {
    renderTimeline([], timelineDetailStub({ hidden: [123] }));
    expect(screen.getByText(/Show hidden/).textContent).toContain("1");
  });

  it("clicking Show Hidden calls setShowHiddenThreads(true)", async () => {
    const detail = timelineDetailStub({ hidden: [123] });
    renderTimeline([], detail);
    await fireEvent.click(screen.getByText(/Show hidden/));
    expect(detail.setShowHiddenThreads).toHaveBeenCalledWith(true);
  });
});
