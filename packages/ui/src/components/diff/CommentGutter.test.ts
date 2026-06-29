import { describe, it, expect, vi, beforeEach } from "vitest";
import { render } from "@testing-library/svelte";
import CommentGutter from "./CommentGutter.svelte";
import type { GutterEntry } from "./CommentGutter.svelte";

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

describe("CommentGutter", () => {
  it("renders one positioned wrapper per entry with data-gutter-key", async () => {
    const entries: GutterEntry[] = [
      {
        key: "e1",
        desiredTop: 100,
        cards: [{ kind: "draft", key: "d:d1", comment: fakeDraft }],
      },
      {
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
      { key: "first", desiredTop: 0, cards: [] },
      { key: "second", desiredTop: 10, cards: [] },
      { key: "third", desiredTop: 20, cards: [] },
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
        entries: [],
        repoOwner: "local",
        repoName: "demo",
        currentHeadSha: "abc",
        ondelete: vi.fn(),
      },
    });

    await new Promise((r) => setTimeout(r, 0));

    expect(container.querySelectorAll("[data-gutter-key]").length).toBe(0);
  });
});
