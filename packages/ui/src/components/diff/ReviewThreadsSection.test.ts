import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, fireEvent } from "@testing-library/svelte";

const applyAll = vi.fn(async () => true);
const deleteThread = vi.fn(async () => true);
const selectCommit = vi.fn(async () => undefined);
const resetToHead = vi.fn(async () => undefined);
const resolveAt = vi.fn(async () => undefined);
const getCurrentPR = vi.fn(() => null);
const getCurrentCommitSha = vi.fn(() => "headsha");
const isFileCollapsed = vi.fn(() => false);
const toggleFileCollapsed = vi.fn();
let running = false;
const threadsRef: { value: unknown[] } = { value: [] };
const commitsRef: { value: unknown } = { value: [] };
const scopeRef: { value: unknown } = { value: { kind: "head" } };

type MockThread = {
  path: string;
  line: number;
  resolved?: { state: string; path?: string; line?: number };
};

// Mirrors the real store's placementFor (reviewThreads.svelte.ts) so the
// component's placeInDiff exercises the same resolved-vs-recorded choice
// under test as it does live.
function placementFor(t: MockThread): { path: string; line: number } {
  const r = t.resolved;
  if (r && (r.state === "current" || r.state === "moved")) {
    return { path: r.path ?? t.path, line: r.line ?? t.line };
  }
  return { path: t.path, line: t.line };
}

// Mirrors the real store's isPlaceable: "removed" and "unmappable" are
// list-only, so the component must not try to jump to them at all.
function isPlaceable(t: MockThread): boolean {
  const state = t.resolved?.state;
  return state === undefined || state === "current" || state === "moved";
}

vi.mock("../../context.js", () => ({
  getStores: () => ({
    reviewThreads: {
      getThreads: () => threadsRef.value,
      applyAll,
      deleteThread,
      placementFor,
      isPlaceable,
      resolveAt,
    },
    worktreeSession: { hasRunningTurn: () => running },
    diff: {
      getCommits: () => commitsRef.value,
      getScope: () => scopeRef.value,
      selectCommit,
      resetToHead,
      getCurrentPR,
      getCurrentCommitSha,
      isFileCollapsed,
      toggleFileCollapsed,
      requestRevealLine: vi.fn(),
      consumeRevealTarget: vi.fn(),
    },
  }),
}));

import ReviewThreadsSection from "./ReviewThreadsSection.svelte";
import { clearSectionHeight, setSectionHeight } from "./sectionHeights.svelte.js";

function thread(over: Record<string, unknown> = {}) {
  return {
    id: 1, path: "a.go", side: "RIGHT", line: 12, commit_sha: "abc",
    status: "open", hidden: false, created_at: "", updated_at: "",
    comments: [{ id: 1, author: "user", body: "rename this please", created_at: "" }],
    ...over,
  };
}

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  running = false;
  threadsRef.value = [];
  commitsRef.value = [];
  scopeRef.value = { kind: "head" };
  clearSectionHeight("threads");
});

beforeEach(() => {
  // jsdom: scrollIntoView is not implemented (see scrollToDiffLine.test.ts).
  Element.prototype.scrollIntoView = vi.fn();
});

describe("ReviewThreadsSection", () => {
  it("renders nothing when there are no threads", () => {
    threadsRef.value = [];
    const { queryByText } = render(ReviewThreadsSection);
    expect(queryByText("Review threads")).toBeNull();
  });

  it("lists non-hidden threads by path, without the comment preview", () => {
    threadsRef.value = [thread(), thread({ id: 2, hidden: true })];
    const { getByText, queryByText, getByTitle } = render(ReviewThreadsSection);
    expect(getByText("Review threads")).toBeTruthy();
    expect(getByText("a.go")).toBeTruthy(); // path shown
    expect(queryByText(/rename this please/)).toBeNull(); // preview removed (#9)
    expect(getByTitle("a.go")).toBeTruthy(); // full path on hover (#7/#9)
    expect(getByText("1")).toBeTruthy(); // count = 1 non-hidden
    expect(queryByText("2")).toBeNull();
  });

  it("shows a status dot instead of the raw status word (#11)", () => {
    threadsRef.value = [thread({ status: "applied" })];
    const { container, queryByText } = render(ReviewThreadsSection);
    expect(container.querySelector(".thread-item__dot--applied")).toBeTruthy();
    expect(queryByText("applied")).toBeNull();
  });

  it("highlights the active thread when its row is clicked (#8)", async () => {
    threadsRef.value = [thread({ id: 1 }), thread({ id: 2, path: "b.go" })];
    const { getByText, container } = render(ReviewThreadsSection);
    expect(container.querySelector(".thread-item-row--active")).toBeNull();
    await fireEvent.click(getByText("a.go"));
    const active = container.querySelector(".thread-item-row--active");
    expect(active).toBeTruthy();
    expect(active?.textContent).toContain("a.go");
  });

  it("deletes a thread from the sidebar after a confirm click (#15)", async () => {
    threadsRef.value = [thread({ id: 7 })];
    const { getByTitle } = render(ReviewThreadsSection);
    await fireEvent.click(getByTitle("Delete this thread"));
    expect(deleteThread).not.toHaveBeenCalled(); // first click arms the confirm
    await fireEvent.click(getByTitle("Click again to delete"));
    expect(deleteThread).toHaveBeenCalledWith(7);
  });

  it("Apply all stays enabled while a turn runs and shows a queue tooltip", async () => {
    threadsRef.value = [thread({ status: "discussed" })];
    running = true;
    const { getByText } = render(ReviewThreadsSection);
    const btn = getByText("Apply all") as HTMLButtonElement;
    expect(btn.disabled).toBe(false);
    expect(btn.getAttribute("title") ?? "").toMatch(/queue/i);
    await fireEvent.click(btn);
    expect(applyAll).toHaveBeenCalled();
  });

  it("Apply all triggers when idle", async () => {
    threadsRef.value = [thread({ status: "open" })];
    running = false;
    const { getByText } = render(ReviewThreadsSection);
    await fireEvent.click(getByText("Apply all"));
    expect(applyAll).toHaveBeenCalled();
  });
});

describe("ReviewThreadsSection — click-to-navigate", () => {
  function commit(over: Record<string, unknown> = {}) {
    return { sha: "abc", subject: "x", parents: [], author: "x", date: "", ...over };
  }

  it("clicks a thread anchored to the PR head → resetToHead", async () => {
    const t = thread({ id: 1, commit_sha: "headsha" });
    threadsRef.value = [t];
    commitsRef.value = [commit({ sha: "headsha" })];
    scopeRef.value = { kind: "commit", sha: "headsha" };

    const { getByTitle } = render(ReviewThreadsSection);
    await fireEvent.click(getByTitle(t.path));

    expect(resetToHead).toHaveBeenCalledOnce();
    expect(selectCommit).not.toHaveBeenCalled();
  });

  it("clicks a thread anchored to a mid-stack commit → resetToHead (not selectCommit)", async () => {
    const t = thread({ id: 1, commit_sha: "midsha" });
    threadsRef.value = [t];
    commitsRef.value = [commit({ sha: "headsha" }), commit({ sha: "midsha" })];
    scopeRef.value = { kind: "commit", sha: "midsha" };

    const { getByTitle } = render(ReviewThreadsSection);
    await fireEvent.click(getByTitle(t.path));

    expect(resetToHead).toHaveBeenCalledOnce();
    expect(selectCommit).not.toHaveBeenCalled();
  });

  it("clicks any thread when already at HEAD scope → no extra resetToHead call (no-op)", async () => {
    const t = thread({ id: 1, commit_sha: "midsha" });
    threadsRef.value = [t];
    commitsRef.value = [commit({ sha: "headsha" }), commit({ sha: "midsha" })];
    scopeRef.value = { kind: "head" };

    const { getByTitle } = render(ReviewThreadsSection);
    await fireEvent.click(getByTitle(t.path));

    expect(resetToHead).not.toHaveBeenCalled();
    expect(selectCommit).not.toHaveBeenCalled();
  });

  it("clicks a thread whose commit_sha is not in the commit list → resetToHead AND row flagged orphan", async () => {
    const t = thread({ id: 1, commit_sha: "rebased-away-sha" });
    threadsRef.value = [t];
    commitsRef.value = [commit({ sha: "headsha" })];
    // Start in commit scope so navigation fires; the orphan dot should always appear.
    scopeRef.value = { kind: "commit", sha: "headsha" };

    const { container, getByTitle } = render(ReviewThreadsSection);
    await fireEvent.click(getByTitle(t.path));

    expect(resetToHead).toHaveBeenCalledOnce();
    expect(selectCommit).not.toHaveBeenCalled();
    expect(container.querySelector(".thread-item__dot--orphan")).toBeTruthy();
  });

  it("does not flag orphan while commits are still loading", () => {
    const t = thread({ id: 1, commit_sha: "anything" });
    threadsRef.value = [t];
    commitsRef.value = null; // loading
    scopeRef.value = { kind: "head" };

    const { container } = render(ReviewThreadsSection);
    expect(container.querySelector(".thread-item__dot--orphan")).toBeNull();
  });

  it("attaches an orphan aria-label to the button for orphan threads", async () => {
    const t = thread({ id: 1, commit_sha: "rebased-away-sha" });
    threadsRef.value = [t];
    commitsRef.value = [commit({ sha: "headsha" })];
    scopeRef.value = { kind: "head" };
    const { getByTitle } = render(ReviewThreadsSection);
    const btn = getByTitle(t.path) as HTMLButtonElement;
    expect(btn.getAttribute("aria-label") ?? "").toMatch(/anchored to a commit no longer in this branch/);
  });

  it("offers a resize boundary below the thread list", () => {
    threadsRef.value = [thread()];
    const { container } = render(ReviewThreadsSection);
    const handle = container.querySelector('[data-section-resize="threads"]');
    expect(handle).not.toBeNull();
    // The boundary must sit after the body, since dragging it sizes the
    // section above it.
    const body = container.querySelector(".threads-section__body");
    expect(body?.nextElementSibling).toBe(handle);
  });

  it("drops the resize boundary while the section is collapsed", async () => {
    threadsRef.value = [thread()];
    const { container, getByText } = render(ReviewThreadsSection);
    expect(container.querySelector('[data-section-resize="threads"]')).not.toBeNull();
    await fireEvent.click(getByText("Review threads"));
    expect(container.querySelector(".threads-section__body")).toBeNull();
    // Nothing to resize once the body is gone.
    expect(container.querySelector('[data-section-resize="threads"]')).toBeNull();
  });

  it("caps the body at a height carried over from an earlier session", () => {
    setSectionHeight("threads", 260);
    threadsRef.value = [thread()];
    const { container } = render(ReviewThreadsSection);
    const body = container.querySelector(".threads-section__body") as HTMLElement;
    expect(body.style.maxHeight).toBe("260px");
  });

  it("leaves the stylesheet default in charge when unsized", () => {
    threadsRef.value = [thread()];
    const { container } = render(ReviewThreadsSection);
    const body = container.querySelector(".threads-section__body") as HTMLElement;
    expect(body.style.maxHeight).toBe("");
  });
});

describe("ReviewThreadsSection reachability", () => {
  it("expands the thread in place when the diff cannot host it", async () => {
    // No .diff-file elements exist in this test DOM, so the jump cannot
    // land -- exactly the case that used to do nothing at all.
    threadsRef.value = [thread({ path: "gone.go", line: 65 })];
    const { getByTitle, container } = render(ReviewThreadsSection);

    expect(container.querySelector(".thread-item__fallback")).toBeNull();
    await fireEvent.click(getByTitle("gone.go"));

    const card = container.querySelector(".thread-item__fallback");
    expect(card).not.toBeNull();
    // The conversation itself, not a placeholder.
    expect(card!.textContent).toContain("rename this please");
  });

  it("says why the thread could not be placed", async () => {
    threadsRef.value = [thread({ path: "gone.go", line: 65 })];
    const { getByTitle, container } = render(ReviewThreadsSection);
    await fireEvent.click(getByTitle("gone.go"));
    expect(container.querySelector(".thread-item__reason")!.textContent)
      .toContain("unchanged here");
  });

  it("says the commit was rebased away when the anchor's sha is gone", async () => {
    const t = thread({ path: "rebased.go", line: 4, commit_sha: "dead" });
    threadsRef.value = [t];
    // Must be non-empty: an empty list reads as "still loading" and skips
    // this check entirely, so a populated list missing the thread's sha
    // is what actually reaches the "rebased away" branch.
    commitsRef.value = [{ sha: "headsha", subject: "x", parents: [], author: "x", date: "" }];
    const { getByTitle, container } = render(ReviewThreadsSection);
    await fireEvent.click(getByTitle("rebased.go"));
    expect(container.querySelector(".thread-item__reason")!.textContent)
      .toContain("commit rebased away");
  });

  it("says the resolved path is unchanged here, not the recorded one, when a rename outruns the rendered file", async () => {
    // The thread's resolved anchor moved to b.go, but this diff currently
    // renders only a.go (the thread's recorded path). placeInDiff looks
    // for b.go and finds nothing; placementReason must ask about that same
    // resolved path (b.go), not the recorded a.go -- otherwise the reason
    // it reports describes a file the thread no longer lives at, even
    // though a.go itself is right there in the DOM.
    const file = document.createElement("div");
    file.className = "diff-file";
    file.dataset.filePath = "a.go";
    document.body.appendChild(file);
    try {
      const t = thread({
        path: "a.go", line: 5,
        resolved: { state: "moved", path: "b.go", line: 8 },
      });
      threadsRef.value = [t];
      const { getByTitle, container } = render(ReviewThreadsSection);
      await fireEvent.click(getByTitle("a.go"));
      expect(container.querySelector(".thread-item__reason")!.textContent)
        .toContain("file unchanged here");
    } finally {
      document.body.innerHTML = "";
    }
  });

  it("collapses the in-place card when the row is clicked again", async () => {
    threadsRef.value = [thread({ path: "gone.go", line: 65 })];
    const { getByTitle, container } = render(ReviewThreadsSection);
    await fireEvent.click(getByTitle("gone.go"));
    expect(container.querySelector(".thread-item__fallback")).not.toBeNull();
    await fireEvent.click(getByTitle("gone.go"));
    expect(container.querySelector(".thread-item__fallback")).toBeNull();
  });

  it("does not expand in place when the jump lands", async () => {
    // A real anchor element in the DOM: the jump succeeds, so the reader
    // is looking at the diff and the sidebar stays a list.
    const file = document.createElement("div");
    file.className = "diff-file";
    file.dataset.filePath = "a.go";
    const line = document.createElement("div");
    line.className = "line-wrap";
    line.dataset.anchorLine = "12";
    line.dataset.anchorSide = "RIGHT";
    file.appendChild(line);
    document.body.appendChild(file);

    // try/finally: teardown must run even if an assertion below throws,
    // or this fixture leaks into every later test in the file.
    try {
      threadsRef.value = [thread({ path: "a.go", line: 12, side: "RIGHT" })];
      const { getByTitle, container } = render(ReviewThreadsSection);
      await fireEvent.click(getByTitle("a.go"));
      expect(container.querySelector(".thread-item__fallback")).toBeNull();
    } finally {
      document.body.innerHTML = "";
    }
  });

  it("expands in place when the file is rendered but the line is not", async () => {
    // The .diff-file exists, so the jump does not report "missing" -- it
    // reports "pending": a reveal was requested and the view moved to the
    // file header. But CollapsedRegion renders only diff lines, never a
    // ReviewThreadCard, so treating "pending" as placed left the reader
    // on a header with no conversation and no reason. A thread at line
    // 200 of a file whose hunks are 1-50 and 300-320 is exactly this.
    const file = document.createElement("div");
    file.className = "diff-file";
    file.dataset.filePath = "a.go";
    const line = document.createElement("div");
    line.className = "line-wrap";
    line.dataset.anchorLine = "12"; // NOT the thread's line
    line.dataset.anchorSide = "RIGHT";
    file.appendChild(line);
    document.body.appendChild(file);

    try {
      threadsRef.value = [thread({ path: "a.go", line: 200, side: "RIGHT" })];
      const { getByTitle, container } = render(ReviewThreadsSection);
      await fireEvent.click(getByTitle("a.go"));

      const card = container.querySelector(".thread-item__fallback");
      expect(card).not.toBeNull();
      expect(card!.textContent).toContain("rename this please");
      expect(container.querySelector(".thread-item__reason")!.textContent)
        .toContain("line not in this diff");
    } finally {
      document.body.innerHTML = "";
    }
  });

  it("expands a removed thread in place without jumping anywhere", async () => {
    // Its recorded line IS rendered here, so the old code would have
    // jumped to it and shown nothing (the store no longer places removed
    // threads). List-only means don't even try.
    const file = document.createElement("div");
    file.className = "diff-file";
    file.dataset.filePath = "a.go";
    const line = document.createElement("div");
    line.className = "line-wrap";
    line.dataset.anchorLine = "12";
    line.dataset.anchorSide = "RIGHT";
    file.appendChild(line);
    document.body.appendChild(file);

    try {
      threadsRef.value = [
        thread({ path: "a.go", line: 12, side: "RIGHT", resolved: { state: "removed" } }),
      ];
      const { getByTitle, container } = render(ReviewThreadsSection);
      await fireEvent.click(getByTitle("a.go"));
      expect(container.querySelector(".thread-item__fallback")).not.toBeNull();
      expect(container.querySelector(".thread-item__reason")!.textContent)
        .toContain("line removed");
    } finally {
      document.body.innerHTML = "";
    }
  });

  it("re-resolves against the new scope before placing from a non-head scope", async () => {
    // resetToHead renumbers the diff, so the resolutions on hand describe
    // the scope just left. Placing against them would put the card where
    // the code was in the OTHER scope.
    threadsRef.value = [thread({ path: "gone.go", line: 65 })];
    scopeRef.value = { kind: "commit", sha: "midsha" };
    const { getByTitle } = render(ReviewThreadsSection);
    await fireEvent.click(getByTitle("gone.go"));
    expect(resetToHead).toHaveBeenCalledOnce();
    expect(resolveAt).toHaveBeenCalledWith("headsha");
  });

  it("keeps every thread openable, whatever state its anchor is in", async () => {
    // The invariant, stated once: each row opens its conversation, either
    // by placing it in the diff or by expanding in place.
    threadsRef.value = [
      thread({ id: 1, path: "gone.go", line: 65 }),
      thread({ id: 2, path: "moved.go", line: 900 }),
      thread({ id: 3, path: "rebased.go", line: 4, commit_sha: "dead" }),
    ];
    const { container, getByTitle } = render(ReviewThreadsSection);
    for (const path of ["gone.go", "moved.go", "rebased.go"]) {
      await fireEvent.click(getByTitle(path));
      expect(
        container.querySelector(".thread-item__fallback"),
        `${path} must be readable`,
      ).not.toBeNull();
      await fireEvent.click(getByTitle(path)); // collapse before the next
    }
  });
});

describe("ReviewThreadsSection — resolved placement labels", () => {
  it("shows a moved marker on the row, titled with the recorded anchor", () => {
    threadsRef.value = [
      thread({
        id: 1, path: "a.go", line: 5, side: "RIGHT",
        resolved: { state: "moved", path: "a.go", line: 8 },
      }),
      thread({ id: 2, path: "b.go", line: 3 }), // no resolution -- no marker
    ];
    const { queryAllByText } = render(ReviewThreadsSection);
    const markers = queryAllByText("moved");
    expect(markers).toHaveLength(1);
    expect(markers[0]!.title).toBe("recorded at +5");
  });

  it("badges every non-current state on the row, not just moved", async () => {
    // A removed or unmappable thread is list-only: it never gets an
    // inline card, so the row is the only place its state can be noticed
    // at all. Badging only "moved" left those two silently unexplained.
    threadsRef.value = [
      thread({ id: 1, path: "a.go", line: 5, resolved: { state: "removed" } }),
      thread({ id: 2, path: "b.go", line: 6, resolved: { state: "unmappable" } }),
      thread({ id: 3, path: "c.go", line: 7, resolved: { state: "current", path: "c.go", line: 7 } }),
      thread({ id: 4, path: "d.go", line: 8 }), // no resolution -- no badge
    ];
    const { container, queryAllByText } = render(ReviewThreadsSection);
    expect(queryAllByText("line removed")).toHaveLength(1);
    expect(queryAllByText("position unknown")).toHaveLength(1);
    // "current" and unresolved rows stay unlabelled: 2 badges in total.
    expect(container.querySelectorAll(".thread-item__drift")).toHaveLength(2);
  });

  it("labels a removed thread's expanded card 'line removed'", async () => {
    const t = thread({ path: "a.go", line: 5, resolved: { state: "removed" } });
    threadsRef.value = [t];
    const { getByTitle, container } = render(ReviewThreadsSection);
    await fireEvent.click(getByTitle("a.go"));
    expect(container.querySelector(".thread-item__reason")!.textContent)
      .toContain("line removed");
  });

  it("labels an unmappable thread's expanded card 'position unknown'", async () => {
    const t = thread({ path: "a.go", line: 5, resolved: { state: "unmappable" } });
    threadsRef.value = [t];
    const { getByTitle, container } = render(ReviewThreadsSection);
    await fireEvent.click(getByTitle("a.go"));
    expect(container.querySelector(".thread-item__reason")!.textContent)
      .toContain("position unknown");
  });
});
