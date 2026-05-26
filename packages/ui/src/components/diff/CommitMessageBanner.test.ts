import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render } from "@testing-library/svelte";
import { STORES_KEY } from "../../context.js";
import CommitMessageBanner from "./CommitMessageBanner.svelte";

// Stubbed diff/commitAnalysis stores wired to the minimum surface
// CommitMessageBanner reads. The banner only renders when the diff
// scope is a single commit and an active CommitInfo + index are
// available; the stubs return all three together so the chevron is
// always visible in tests.
function diffStub() {
  return {
    getScope: () => ({ kind: "commit" as const, sha: "abc1234" }),
    getActiveCommit: () => ({
      sha: "abc1234deadbeef",
      message: "feat: do the thing",
      body: "",
      author_name: "alice",
    }),
    getCommitIndex: () => ({ current: 1, total: 1 }),
  };
}

function caStub() {
  return {
    setPR: vi.fn(),
    fetchFor: vi.fn(async () => {}),
    get: () => null,
    isInFlight: () => false,
    generate: vi.fn(async () => {}),
    remove: vi.fn(async () => {}),
  };
}

function renderBanner(props: Record<string, unknown> = {}) {
  return render(CommitMessageBanner, {
    props: { owner: "acme", name: "widget", number: 1, ...props },
    context: new Map<symbol, unknown>([
      [STORES_KEY, { diff: diffStub(), commitAnalysis: caStub() }],
    ]),
  });
}

beforeEach(() => {
  localStorage.clear();
});

afterEach(() => {
  cleanup();
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
