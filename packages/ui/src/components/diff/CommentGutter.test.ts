import { describe, it, expect, vi, beforeEach } from "vitest";
import { render } from "@testing-library/svelte";
import CommentGutter from "./CommentGutter.svelte";
import type { GutterEntry } from "./CommentGutter.svelte";
import { STORES_KEY } from "../../context.js";
import type { ReviewThread } from "../../stores/reviewThreads.svelte.js";

// Stub the card components so they render without pulling in store context
// or layout-dependent code. Pattern mirrors DocReviewSurface.test.ts.
vi.mock("./AIThreadCard.svelte", () => ({
  default: vi.fn().mockImplementation(() => ({ $$: {} })),
}));
vi.mock("./ReviewCommentCard.svelte", () => ({
  default: vi.fn().mockImplementation(() => ({ $$: {} })),
}));
vi.mock("./PendingCommentCard.svelte", () => ({
  default: vi.fn().mockImplementation(() => ({ $$: {} })),
}));

beforeEach(() => {
  // jsdom: scrollIntoView is not implemented
  Element.prototype.scrollIntoView = vi.fn();
});

// Minimal fake comment/thread shapes — just enough for the props the gutter
// passes through. The card components are mocked, so field contents don't
// execute real logic.
const fakeDraft = {
  id: "d1",
  path: "README.md",
  line: 5,
  side: "RIGHT" as const,
  commitSha: "abc",
  body: "draft body",
  createdAt: new Date().toISOString(),
};

const fakePublished = {
  id: 1,
  path: "README.md",
  line: 10,
  side: "RIGHT" as const,
  commitId: "abc",
  body: "published body",
  author: "alice",
  createdAt: new Date().toISOString(),
  inReplyTo: 0,
  isHidden: false,
};

const fakeThread = {
  id: 99,
  mr_id: 1,
  path: "README.md",
  anchor_line: 5,
  anchor_side: "RIGHT" as const,
  commit_sha: "abc",
  status: "open",
  created_at: new Date().toISOString(),
};

// A persisted review thread — unlike fakeThread (an Ask-Claude AIThread), this
// is a real ReviewThreadCard-shaped thread. ReviewThreadCard is NOT mocked
// (the test below asserts its real markup), so it self-injects `reviewThreads`
// + `worktreeSession` via getStores() — the test provides both through a
// STORES_KEY context, mirroring ReviewThreadCard.test.ts's own stub surface.
const fakeReviewThread: ReviewThread = {
  id: 42,
  path: "README.md",
  side: "RIGHT",
  line: 5,
  commit_sha: "abc1234",
  status: "open",
  hidden: false,
  created_at: "",
  updated_at: "",
  writes_allowed: false,
  comments: [{ id: 1, author: "user", body: "seeded thread comment", created_at: "", sent_to_agent: false }],
};

function reviewThreadStoresContext() {
  return new Map([
    [
      STORES_KEY,
      {
        reviewThreads: {
          resolve: vi.fn(async () => true),
          unresolve: vi.fn(async () => true),
          hide: vi.fn(async () => true),
          unhide: vi.fn(async () => true),
          addComment: vi.fn(async () => true),
          apply: vi.fn(async () => true),
          deleteThread: vi.fn(async () => true),
          ask: vi.fn(async () => true),
          discuss: vi.fn(async () => true),
        },
        worktreeSession: { hasRunningTurn: () => false },
      },
    ],
  ]);
}

describe("CommentGutter", () => {
  it("renders one positioned wrapper per entry with data-gutter-key", async () => {
    const entries: GutterEntry[] = [
      {
        kind: "cards",
        key: "e1",
        desiredTop: 100,
        cards: [{ kind: "draft", key: "d:d1", comment: fakeDraft }],
      },
      {
        kind: "cards",
        key: "e2",
        desiredTop: 200,
        cards: [{ kind: "published", key: "p:1", comment: fakePublished }],
      },
    ];

    const { container } = render(CommentGutter, {
      props: {
        entries,
        repoOwner: "local",
        repoName: "demo",
        currentHeadSha: "abc",
        ondelete: vi.fn(),
      },
    });

    // Flush any pending microtasks (effects that set top).
    await new Promise((r) => setTimeout(r, 0));

    const wrappers = container.querySelectorAll<HTMLElement>("[data-gutter-key]");
    expect(wrappers.length).toBe(2);
    expect(wrappers[0]!.getAttribute("data-gutter-key")).toBe("e1");
    expect(wrappers[1]!.getAttribute("data-gutter-key")).toBe("e2");
  });

  it("each wrapper has a style:top applied (position-before-paint, even at 0px in jsdom)", async () => {
    const entries: GutterEntry[] = [
      {
        kind: "cards",
        key: "k1",
        desiredTop: 50,
        cards: [{ kind: "ai", key: "a:99", thread: fakeThread }],
      },
    ];

    const { container } = render(CommentGutter, {
      props: {
        entries,
        repoOwner: "local",
        repoName: "demo",
        currentHeadSha: "abc",
        ondelete: vi.fn(),
      },
    });

    // Give effects one tick to fire.
    await new Promise((r) => setTimeout(r, 0));

    const wrapper = container.querySelector<HTMLElement>("[data-gutter-key='k1']");
    expect(wrapper).toBeTruthy();
    // jsdom returns 0 for all offsetHeight so resolveStack places things at
    // their desiredTop. The style attribute must exist and include 'top:'.
    expect(wrapper!.style.top).toBeDefined();
    // After a resolveStack pass, desiredTop=50 with height=0 and gap=8 →
    // top stays at 50px (nothing to push). Accept any px value — what matters
    // is the attribute is set, not the exact pixel in jsdom.
    expect(wrapper!.style.top).toMatch(/^\d+px$/);
  });

  it("renders wrappers in entry order", async () => {
    const entries: GutterEntry[] = [
      { kind: "cards", key: "first", desiredTop: 0, cards: [] },
      { kind: "cards", key: "second", desiredTop: 10, cards: [] },
      { kind: "cards", key: "third", desiredTop: 20, cards: [] },
    ];

    const { container } = render(CommentGutter, {
      props: {
        entries,
        repoOwner: "local",
        repoName: "demo",
        currentHeadSha: "abc",
        ondelete: vi.fn(),
      },
    });

    await new Promise((r) => setTimeout(r, 0));

    const keys = Array.from(
      container.querySelectorAll("[data-gutter-key]"),
    ).map((el) => el.getAttribute("data-gutter-key"));
    expect(keys).toEqual(["first", "second", "third"]);
  });

  it("renders no wrappers when entries is empty", async () => {
    const { container } = render(CommentGutter, {
      props: {
        entries: [] as GutterEntry[],
        repoOwner: "local",
        repoName: "demo",
        currentHeadSha: "abc",
        ondelete: vi.fn(),
      },
    });

    await new Promise((r) => setTimeout(r, 0));

    expect(container.querySelectorAll("[data-gutter-key]").length).toBe(0);
  });

  it("renders a review-thread card via ReviewThreadCard", async () => {
    const entries: GutterEntry[] = [
      {
        kind: "cards",
        key: "e3",
        desiredTop: 0,
        cards: [{ kind: "review-thread", key: "rt:42", thread: fakeReviewThread }],
      },
    ];

    const { container } = render(CommentGutter, {
      props: {
        entries,
        repoOwner: "local",
        repoName: "demo",
        currentHeadSha: "abc",
        ondelete: vi.fn(),
      },
      context: reviewThreadStoresContext(),
    });

    await new Promise((r) => setTimeout(r, 0));

    const card = container.querySelector(".review-thread");
    expect(card).toBeTruthy();
    expect(card?.querySelector(".review-thread__badge")?.textContent).toBe("Review");
    expect(card?.textContent).toContain("seeded thread comment");
  });
});
