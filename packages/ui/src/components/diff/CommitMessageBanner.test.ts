import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render } from "@testing-library/svelte";
import { STORES_KEY } from "../../context.js";
import CommitMessageBanner from "./CommitMessageBanner.svelte";
import {
  clearSectionHeight,
  setSectionHeight,
} from "./sectionHeights.svelte.js";

// Stubbed diff/commitAnalysis stores wired to the minimum surface
// CommitMessageBanner reads. The banner only renders when the diff
// scope is a single commit and an active CommitInfo + index are
// available; the stubs return all three together so the chevron is
// always visible in tests.
function diffStub(body = "") {
  return {
    getScope: () => ({ kind: "commit" as const, sha: "abc1234" }),
    getActiveCommit: () => ({
      sha: "abc1234deadbeef",
      message: "feat: do the thing",
      body,
      author_name: "alice",
    }),
    getCommitIndex: () => ({ current: 1, total: 1 }),
  };
}

function caStub(content: string | null = null) {
  const analysis = content === null
    ? null
    : { status: "done", content, error: "" };
  return {
    setPR: vi.fn(),
    fetchFor: vi.fn(async () => {}),
    get: () => analysis,
    isInFlight: () => false,
    generate: vi.fn(async () => {}),
    remove: vi.fn(async () => {}),
  };
}

interface Content {
  body?: string;
  analysis?: string | null;
}

function renderBanner(props: Record<string, unknown> = {}, content: Content = {}) {
  return render(CommitMessageBanner, {
    props: { owner: "acme", name: "widget", number: 1, ...props },
    context: new Map<symbol, unknown>([
      [STORES_KEY, {
        diff: diffStub(content.body ?? ""),
        commitAnalysis: caStub(content.analysis ?? null),
      }],
    ]),
  });
}

beforeEach(() => {
  localStorage.clear();
});

afterEach(() => {
  cleanup();
  clearSectionHeight("commit-message");
  clearSectionHeight("commit-analysis");
});

describe("CommitMessageBanner chevron", () => {
  it("chevron shows expanded state when forceExpanded=true even if pr-commit-msg-collapsed=true", () => {
    // Peeking a previously collapsed commit-message banner must flip
    // the chevron rotation and title text to match the visible body.
    localStorage.setItem("pr-commit-msg-collapsed", "true");
    const { container } = renderBanner({ forceExpanded: true });
    expect(container.querySelector(".commit-banner__chevron--collapsed")).toBeNull();
  });

  it("chevron is collapsed when pr-commit-msg-collapsed=true and forceExpanded is omitted", () => {
    localStorage.setItem("pr-commit-msg-collapsed", "true");
    const { container } = renderBanner();
    expect(container.querySelector(".commit-banner__chevron--collapsed")).toBeTruthy();
  });
});

describe("CommitMessageBanner resize boundaries", () => {
  const MESSAGE = "why this change was necessary, at length";
  const SUMMARY = "the model's read on the commit";

  it("offers a boundary under the commit message body", () => {
    const { container } = renderBanner({}, { body: MESSAGE });
    const body = container.querySelector(".commit-banner__body");
    const handle = container.querySelector('[data-section-resize="commit-message"]');
    expect(handle).not.toBeNull();
    // Dragging a boundary sizes the body above it, so the order matters.
    expect(body?.nextElementSibling).toBe(handle);
  });

  it("gives the AI summary its own boundary, independent of the message's", () => {
    const { container } = renderBanner({}, { body: MESSAGE, analysis: SUMMARY });
    const summary = container.querySelector(".commit-banner__analysis-body");
    const handle = container.querySelector('[data-section-resize="commit-analysis"]');
    expect(handle).not.toBeNull();
    expect(summary?.nextElementSibling).toBe(handle);
    // Both boundaries coexist: a short message and a long summary can be
    // sized against each other.
    expect(container.querySelector('[data-section-resize="commit-message"]')).not.toBeNull();
  });

  it("offers no message boundary when the commit has no body", () => {
    const { container } = renderBanner({}, { analysis: SUMMARY });
    expect(container.querySelector(".commit-banner__body")).toBeNull();
    expect(container.querySelector('[data-section-resize="commit-message"]')).toBeNull();
    // The summary is still there and still resizable.
    expect(container.querySelector('[data-section-resize="commit-analysis"]')).not.toBeNull();
  });

  it("drops both boundaries while the banner is collapsed", () => {
    localStorage.setItem("pr-commit-msg-collapsed", "true");
    const { container } = renderBanner({}, { body: MESSAGE, analysis: SUMMARY });
    expect(container.querySelector('[data-section-resize="commit-message"]')).toBeNull();
    expect(container.querySelector('[data-section-resize="commit-analysis"]')).toBeNull();
  });

  it("caps the message and the summary from separate stored heights", () => {
    setSectionHeight("commit-message", 140);
    setSectionHeight("commit-analysis", 420);
    const { container } = renderBanner({}, { body: MESSAGE, analysis: SUMMARY });
    const message = container.querySelector(".commit-banner__body") as HTMLElement;
    const summary = container.querySelector(".commit-banner__analysis-body") as HTMLElement;
    expect(message.style.maxHeight).toBe("140px");
    expect(summary.style.maxHeight).toBe("420px");
  });

  it("leaves the stylesheet defaults in charge when unsized", () => {
    const { container } = renderBanner({}, { body: MESSAGE, analysis: SUMMARY });
    const message = container.querySelector(".commit-banner__body") as HTMLElement;
    const summary = container.querySelector(".commit-banner__analysis-body") as HTMLElement;
    expect(message.style.maxHeight).toBe("");
    expect(summary.style.maxHeight).toBe("");
  });
});
