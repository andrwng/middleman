import { beforeEach, describe, it, expect, vi } from "vitest";
import { render, fireEvent } from "@testing-library/svelte";
import RenderedMarkdownView from "./RenderedMarkdownView.svelte";
import { STORES_KEY } from "../../context.js";
import { createDiffStore } from "../../stores/diff.svelte.js";
import { createAIStore } from "../../stores/ai.svelte.js";
import { createDetailStore } from "../../stores/detail.svelte.js";
import type { MiddlemanClient } from "../../types.js";
import type { AIThread, AIQuestion } from "../../stores/ai.svelte.js";

function stubClient(): MiddlemanClient {
  return {
    GET: vi.fn(async () => ({ data: undefined, error: undefined })),
    POST: vi.fn(async () => ({ data: undefined, error: undefined })),
    DELETE: vi.fn(async () => ({ data: undefined, error: undefined })),
  } as unknown as MiddlemanClient;
}

// Two-paragraph markdown; first para spans line 1, second spans line 2.
const SAMPLE_MD = "Line one.\nLine two.\n";

beforeEach(() => {
  globalThis.fetch = vi.fn(async () => ({
    ok: true,
    status: 200,
    json: async () => ({
      content: SAMPLE_MD,
      truncated: false,
    }),
  }) as unknown as Response);

  // jsdom does not implement scrollIntoView; stub it so that composer
  // components that call textarea.scrollIntoView() in a $effect don't throw.
  Element.prototype.scrollIntoView = vi.fn();

  // Reset localStorage so draft state from one test does not leak
  // into the next via the diff store's localStorage-backed draft map.
  localStorage.clear();
});

function makeStores() {
  const diff = createDiffStore({ client: stubClient() });
  const ai = createAIStore();
  const detail = createDetailStore({ client: null as unknown as MiddlemanClient });
  return { diff, ai, detail };
}

function renderViewWithStores(stores: ReturnType<typeof makeStores>, commentLayout?: "inline" | "gutter") {
  return render(RenderedMarkdownView, {
    props: {
      owner: "local",
      name: "demo",
      number: 1,
      path: "doc.md",
      sha: "abc",
      hunks: [],
      ...(commentLayout ? { commentLayout } : {}),
    },
    context: new Map([[STORES_KEY, stores]]),
  });
}

function renderView() {
  return renderViewWithStores(makeStores());
}

function renderViewGutter(stores: ReturnType<typeof makeStores>) {
  return renderViewWithStores(stores, "gutter");
}

// Flush all pending microtasks and one macrotask tick so that
// $effects and async fetch both settle.
async function settle() {
  await new Promise((r) => setTimeout(r, 0));
}

describe("RenderedMarkdownView", () => {
  it("renders per-line anchor spans in the body", async () => {
    const { container } = renderView();
    await settle();
    const anchors = container.querySelectorAll(".rmd-anchor");
    expect(anchors.length).toBeGreaterThanOrEqual(2);
    expect(anchors[0]?.getAttribute("data-anchor-line")).toBe("1");
    expect(anchors[1]?.getAttribute("data-anchor-line")).toBe("2");
  });

  it("composer wrap carries a data-rmd-block-idx attribute matching the clicked block", async () => {
    const { container } = renderView();
    // Wait for fetch + DOM effects to settle.
    await settle();

    // The $effect adds .rmd-add-comment-btn imperatively to each block.
    const buttons = container.querySelectorAll(".rmd-add-comment-btn");
    expect(buttons.length).toBeGreaterThanOrEqual(1);

    const firstBtn = buttons[0] as HTMLElement;
    await fireEvent.click(firstBtn);

    // The composer wrap should now be in the DOM.
    const wrap = container.querySelector(".rmd-composer-wrap");
    expect(wrap).toBeTruthy();

    // It must carry a data-rmd-block-idx that ties it to block 0
    // (the first non-space top-level token, i.e. the first paragraph).
    expect(wrap?.getAttribute("data-rmd-block-idx")).toBe("0");
  });

  it("Ask-Claude composer wrap carries a data-rmd-block-idx attribute matching the clicked block", async () => {
    const { container } = renderView();
    await settle();

    const buttons = container.querySelectorAll(".rmd-ask-ai-btn");
    expect(buttons.length).toBeGreaterThanOrEqual(1);

    const firstBtn = buttons[0] as HTMLElement;
    await fireEvent.click(firstBtn);

    const wrap = container.querySelector(".rmd-composer-wrap");
    expect(wrap).toBeTruthy();
    expect(wrap?.getAttribute("data-rmd-block-idx")).toBe("0");
  });

  // Step 1: (B) — draft card injection after mount
  //
  // After the component mounts and renders, we add a draft comment via the
  // same diffStore the component holds. The card-injection $effect depends on
  // `drafts` ($derived from getDraftCommentsForPath). If the effect correctly
  // re-runs on a post-mount store mutation, a .rmd-thread-wrap element should
  // appear inline under the block that owns the anchor line.
  //
  // FAIL result → (B) is a real reactivity/injection bug.
  // PASS result → (B) is a visibility artifact of (A) (scroll to bottom hides
  //               the inline card that was correctly injected up-page).
  it("(B) draft card appears inline after addDraftComment on a mounted view", async () => {
    const stores = makeStores();
    // setActivePR so draftKey() uses the same owner/name/number
    // the component was given, ensuring addDraftComment writes to
    // the key getDraftCommentsForPath reads.
    stores.diff.setActivePR("local", "demo", 1);

    const { container } = renderViewWithStores(stores);
    await settle();

    // Sanity: the body rendered with block elements.
    const body = container.querySelector(".rmd-body");
    expect(body).toBeTruthy();

    // No draft cards yet.
    expect(container.querySelector(".rmd-thread-wrap")).toBeNull();

    // Add a draft for line 1, side RIGHT (first paragraph's source line).
    stores.diff.addDraftComment({
      path: "doc.md",
      line: 1,
      side: "RIGHT",
      commitSha: "abc",
      body: "test draft",
    });

    // Flush reactive effects.
    await settle();

    // The card-injection $effect should have re-run and injected a
    // .rmd-thread-wrap containing a .rmd-thread-host for the draft.
    const wrap = container.querySelector(".rmd-thread-wrap");
    expect(wrap).toBeTruthy();
    const host = container.querySelector(".rmd-thread-host");
    expect(host).toBeTruthy();
  });

  // Step 1: (B) — AI thread card injection after mount
  //
  // Same structure as the draft test above, but for AI threads.
  // createThread makes a real fetch POST; we mock it to return a
  // minimal AiThreadResponse so the store updates threads[].
  it("(B) AI thread card appears inline after createThread on a mounted view", async () => {
    const stores = makeStores();
    stores.diff.setActivePR("local", "demo", 1);

    // Override fetch to serve the blob first, then the thread creation.
    // The blob fetch fires on mount; the thread POST fires later.
    let blobFetched = false;
    globalThis.fetch = vi.fn(async (input: unknown) => {
      const url = typeof input === "string" ? input : (input as Request).url ?? "";
      if (!blobFetched && url.includes("/blob")) {
        blobFetched = true;
        return {
          ok: true,
          status: 200,
          json: async () => ({ content: SAMPLE_MD, truncated: false }),
        } as Response;
      }
      // Thread creation POST.
      const thread: AIThread = {
        id: 42,
        mr_id: 1,
        path: "doc.md",
        anchor_line: 1,
        anchor_side: "RIGHT",
        commit_sha: "abc",
        status: "open",
        created_at: new Date().toISOString(),
      };
      const question: AIQuestion = {
        id: 1,
        thread_id: 42,
        question: "what?",
        answer: "",
        citations_json: "[]",
        status: "queued",
        created_at: new Date().toISOString(),
      };
      return {
        ok: true,
        status: 200,
        json: async () => ({ thread, question }),
      } as Response;
    });

    const { container } = renderViewWithStores(stores);
    await settle();

    expect(container.querySelector(".rmd-thread-wrap")).toBeNull();

    // createThread writes to threads[] inside the AI store.
    const result = await stores.ai.createThread({
      path: "doc.md",
      anchor_side: "RIGHT",
      anchor_line: 1,
      commit_sha: "abc",
      question: "what?",
    });
    expect(result.ok).toBe(true);

    await settle();

    const wrap = container.querySelector(".rmd-thread-wrap");
    expect(wrap).toBeTruthy();
    const host = container.querySelector(".rmd-thread-host");
    expect(host).toBeTruthy();
  });

  // Step 2: (A) — composerTop is set synchronously when the composer opens
  //
  // When openComposerForBlock fires (via clicking the add-comment button),
  // composerTop must be computed immediately so the composer's first render
  // uses the positioned layout. In jsdom, offsetTop/offsetHeight return 0,
  // so composerTop lands at 0 (a number, not null). The important assertion
  // is that the wrap has the --positioned class on FIRST render, not after a
  // delayed effect.
  it("(A) composer wrap gets --positioned class on first render (no flash to bottom)", async () => {
    const { container } = renderView();
    await settle();

    const buttons = container.querySelectorAll(".rmd-add-comment-btn");
    expect(buttons.length).toBeGreaterThanOrEqual(1);

    await fireEvent.click(buttons[0] as HTMLElement);

    // Check immediately after click, before any additional async flushes.
    // If composerTop is computed synchronously in openComposerForBlock,
    // the wrap already has --positioned on its first render.
    const wrap = container.querySelector(".rmd-composer-wrap");
    expect(wrap).toBeTruthy();
    expect(wrap?.classList.contains("rmd-composer-wrap--positioned")).toBe(true);
  });

  it("(A) Ask-Claude composer wrap gets --positioned class on first render", async () => {
    const { container } = renderView();
    await settle();

    const buttons = container.querySelectorAll(".rmd-ask-ai-btn");
    expect(buttons.length).toBeGreaterThanOrEqual(1);

    await fireEvent.click(buttons[0] as HTMLElement);

    const wrap = container.querySelector(".rmd-composer-wrap");
    expect(wrap).toBeTruthy();
    expect(wrap?.classList.contains("rmd-composer-wrap--positioned")).toBe(true);
  });

  // Block range boundary tests: a comment anchored to a block at a boundary
  // must render exactly one inline card, not two.
  //
  // Root cause (pre-fix): marked v17 includes a token's trailing blank line(s)
  // in token.raw. The block endLine was computed as
  //   currentBlockStart + countNewlines(rawText)
  // so a heading "# Hello\n\n" got endLine=3 — its half-open range [1,3) abutted
  // the paragraph's [3,4), and a comment saved via the heading's + button
  // (which uses endLine=3 as anchor) matched BOTH blocks through
  // anchorOverlapsBlock's >= boundary, injecting two .rmd-thread-wrap elements.
  //
  // The fix strips trailing blank lines from rawText before computing endLine,
  // so the heading ends at 2 (not 3) and its range no longer abuts the next block.

  it("a comment on a heading renders exactly one inline card (no boundary double-injection)", async () => {
    const stores = makeStores();
    stores.diff.setActivePR("local", "demo", 1);

    // doc: "# Hello\n\nsome text here\n" → heading on line 1, paragraph on line 3.
    globalThis.fetch = vi.fn(async () => ({
      ok: true,
      status: 200,
      json: async () => ({ content: "# Hello\n\nsome text here\n", truncated: false }),
    }) as unknown as Response);

    const { container } = renderViewWithStores(stores);
    await settle();

    expect(container.querySelectorAll(".rmd-thread-wrap").length).toBe(0);

    // Click the + button on the heading block (index 0) to open the composer.
    const addBtns = container.querySelectorAll(".rmd-add-comment-btn");
    expect(addBtns.length).toBeGreaterThanOrEqual(1);
    await fireEvent.click(addBtns[0] as HTMLElement);
    await settle();

    // Type something into the composer's textarea.
    const textarea = container.querySelector<HTMLTextAreaElement>(".composer__textarea");
    expect(textarea).toBeTruthy();
    await fireEvent.input(textarea!, { target: { value: "heading comment" } });
    await settle();

    // Click "Save draft" to call saveDraft, which uses the block's endLine as anchor.
    const saveBtn = container.querySelector<HTMLButtonElement>(".composer__btn--primary");
    expect(saveBtn).toBeTruthy();
    await fireEvent.click(saveBtn!);
    await settle();

    // The draft is now in the store; the card-injection $effect re-runs.
    // With the boundary double-injection bug, the heading's endLine is inflated
    // to include the trailing blank line separator, making the saved anchor
    // match BOTH the heading block and the paragraph — rendering 2 cards.
    // After the fix the heading's endLine is trimmed, so only 1 card appears.
    expect(container.querySelectorAll(".rmd-thread-wrap").length).toBe(1);
  });

  it("a comment on the paragraph renders exactly one inline card", async () => {
    const stores = makeStores();
    stores.diff.setActivePR("local", "demo", 1);

    globalThis.fetch = vi.fn(async () => ({
      ok: true,
      status: 200,
      json: async () => ({ content: "# Hello\n\nsome text here\n", truncated: false }),
    }) as unknown as Response);

    const { container } = renderViewWithStores(stores);
    await settle();

    expect(container.querySelectorAll(".rmd-thread-wrap").length).toBe(0);

    // Comment anchored to line 3 (the paragraph's own source line).
    stores.diff.addDraftComment({
      path: "doc.md",
      line: 3,
      side: "RIGHT",
      commitSha: "abc",
      body: "para comment",
    });
    await settle();

    expect(container.querySelectorAll(".rmd-thread-wrap").length).toBe(1);
  });

  it("a comment on a single-line doc renders exactly one inline card", async () => {
    const stores = makeStores();
    stores.diff.setActivePR("local", "demo", 1);

    globalThis.fetch = vi.fn(async () => ({
      ok: true,
      status: 200,
      json: async () => ({ content: "only line\n", truncated: false }),
    }) as unknown as Response);

    const { container } = renderViewWithStores(stores);
    await settle();

    expect(container.querySelectorAll(".rmd-thread-wrap").length).toBe(0);

    stores.diff.addDraftComment({
      path: "doc.md",
      line: 1,
      side: "RIGHT",
      commitSha: "abc",
      body: "single line comment",
    });
    await settle();

    expect(container.querySelectorAll(".rmd-thread-wrap").length).toBe(1);
  });

  it("a comment on the last block (no trailing newline) renders exactly one inline card", async () => {
    const stores = makeStores();
    stores.diff.setActivePR("local", "demo", 1);

    // No trailing newline on the last block.
    globalThis.fetch = vi.fn(async () => ({
      ok: true,
      status: 200,
      json: async () => ({ content: "first para\n\nlast para", truncated: false }),
    }) as unknown as Response);

    const { container } = renderViewWithStores(stores);
    await settle();

    expect(container.querySelectorAll(".rmd-thread-wrap").length).toBe(0);

    // Comment on line 3 (start of the last paragraph, no trailing newline).
    stores.diff.addDraftComment({
      path: "doc.md",
      line: 3,
      side: "RIGHT",
      commitSha: "abc",
      body: "last block comment",
    });
    await settle();

    expect(container.querySelectorAll(".rmd-thread-wrap").length).toBe(1);
  });

  it("adjacent blocks (no blank line between) render exactly one inline card", async () => {
    const stores = makeStores();
    stores.diff.setActivePR("local", "demo", 1);

    // A heading immediately followed by a paragraph with NO blank line between
    // them parses as two adjacent block tokens (heading on line 1, paragraph on
    // line 2). The boundary range math must give them non-overlapping half-open
    // ranges so a comment on the heading matches only the heading, not the
    // adjacent paragraph. This is the "adjacent blocks" shape the spec called
    // out; the trailing-blank tests above don't exercise zero-gap adjacency.
    globalThis.fetch = vi.fn(async () => ({
      ok: true,
      status: 200,
      json: async () => ({ content: "# Heading\nadjacent paragraph\n", truncated: false }),
    }) as unknown as Response);

    const { container } = renderViewWithStores(stores);
    await settle();

    expect(container.querySelectorAll(".rmd-thread-wrap").length).toBe(0);

    // Anchored to line 1 (the heading); must not also match the paragraph on line 2.
    stores.diff.addDraftComment({
      path: "doc.md",
      line: 1,
      side: "RIGHT",
      commitSha: "abc",
      body: "adjacent heading comment",
    });
    await settle();

    expect(container.querySelectorAll(".rmd-thread-wrap").length).toBe(1);
  });

  // Gutter mode tests (commentLayout="gutter")
  //
  // In gutter mode the card-injection $effect must NOT inject inline
  // .rmd-thread-wrap elements. Instead a CommentGutter (.comment-gutter)
  // column is rendered, and cards appear inside it as [data-gutter-key]
  // entries. The inline path is exercised by all tests above; these tests
  // exercise only the gutter branch.

  it("(gutter) draft renders inside .comment-gutter — not as inline .rmd-thread-wrap", async () => {
    const stores = makeStores();
    stores.diff.setActivePR("local", "demo", 1);

    const { container } = renderViewGutter(stores);
    await settle();

    // Add a draft so a card exists.
    stores.diff.addDraftComment({
      path: "doc.md",
      line: 1,
      side: "RIGHT",
      commitSha: "abc",
      body: "gutter draft",
    });
    await settle();

    // Must NOT inject an inline thread-wrap.
    expect(container.querySelector(".rmd-thread-wrap")).toBeNull();

    // The gutter container must be present.
    const gutter = container.querySelector(".comment-gutter");
    expect(gutter).toBeTruthy();

    // The gutter must contain at least one entry.
    expect(gutter!.querySelector("[data-gutter-key]")).toBeTruthy();
  });

  it("(gutter) a block with comments carries .rmd-block--commented", async () => {
    const stores = makeStores();
    stores.diff.setActivePR("local", "demo", 1);

    const { container } = renderViewGutter(stores);
    await settle();

    // No comments yet — no block should carry the marker.
    expect(container.querySelector(".rmd-block--commented")).toBeNull();

    stores.diff.addDraftComment({
      path: "doc.md",
      line: 1,
      side: "RIGHT",
      commitSha: "abc",
      body: "marker test",
    });
    await settle();

    // At least one block should now carry .rmd-block--commented.
    const marked = container.querySelector(".rmd-block--commented");
    expect(marked).toBeTruthy();
    // A draft is a review comment → blue (comment) marker, not the ask marker.
    expect(marked!.classList.contains("rmd-block--commented-comment")).toBe(true);
    expect(marked!.classList.contains("rmd-block--commented-ask")).toBe(false);
  });

  it("(gutter) a block with an Ask-Claude thread carries the amber .rmd-block--commented-ask marker", async () => {
    const stores = makeStores();
    stores.diff.setActivePR("local", "demo", 1);

    // Serve the blob on mount, then a thread on createThread.
    let blobFetched = false;
    globalThis.fetch = vi.fn(async (input: unknown) => {
      const url = typeof input === "string" ? input : (input as Request).url ?? "";
      if (!blobFetched && url.includes("/blob")) {
        blobFetched = true;
        return {
          ok: true,
          status: 200,
          json: async () => ({ content: SAMPLE_MD, truncated: false }),
        } as Response;
      }
      const thread: AIThread = {
        id: 77,
        mr_id: 1,
        path: "doc.md",
        anchor_line: 1,
        anchor_side: "RIGHT",
        commit_sha: "abc",
        status: "open",
        created_at: new Date().toISOString(),
      };
      const question: AIQuestion = {
        id: 1,
        thread_id: 77,
        question: "what?",
        answer: "",
        citations_json: "[]",
        status: "queued",
        created_at: new Date().toISOString(),
      };
      return { ok: true, status: 200, json: async () => ({ thread, question }) } as Response;
    });

    const { container } = renderViewGutter(stores);
    await settle();

    await stores.ai.createThread({
      path: "doc.md",
      anchor_side: "RIGHT",
      anchor_line: 1,
      commit_sha: "abc",
      question: "what?",
    });
    await settle();

    const marked = container.querySelector(".rmd-block--commented");
    expect(marked).toBeTruthy();
    // An AI thread → amber (ask) marker, not the comment marker.
    expect(marked!.classList.contains("rmd-block--commented-ask")).toBe(true);
    expect(marked!.classList.contains("rmd-block--commented-comment")).toBe(false);
  });

  it("(gutter) hovering cross-links a card and its source block, and the jump button scrolls", async () => {
    const stores = makeStores();
    stores.diff.setActivePR("local", "demo", 1);

    const { container } = renderViewGutter(stores);
    await settle();

    stores.diff.addDraftComment({
      path: "doc.md",
      line: 1,
      side: "RIGHT",
      commitSha: "abc",
      body: "hover link",
    });
    await settle();

    const block = container.querySelector<HTMLElement>(".rmd-body [data-block-key]");
    const entry = container.querySelector<HTMLElement>(".comment-gutter__entry[data-gutter-key]");
    const gutter = container.querySelector<HTMLElement>(".comment-gutter");
    expect(block).toBeTruthy();
    expect(entry).toBeTruthy();
    expect(gutter).toBeTruthy();
    // The shared key value ties the card to its source block.
    expect(entry!.dataset.gutterKey).toBe(block!.dataset.blockKey);
    expect(block!.classList.contains("rmd-block--linked")).toBe(false);

    // Hovering the card highlights the source block (delegated mouseover).
    await fireEvent.mouseOver(entry!);
    await settle();
    expect(block!.classList.contains("rmd-block--linked")).toBe(true);
    // Leaving the gutter clears it.
    await fireEvent.mouseLeave(gutter!);
    await settle();
    expect(block!.classList.contains("rmd-block--linked")).toBe(false);

    // Reverse: hovering the block highlights the gutter card (the block uses an
    // imperative onmouseenter handler).
    await fireEvent.mouseEnter(block!);
    await settle();
    expect(entry!.classList.contains("comment-gutter__entry--linked")).toBe(true);
    await fireEvent.mouseLeave(block!);
    await settle();

    // The per-card jump button scrolls the source block into view.
    const scrollSpy = vi.spyOn(Element.prototype, "scrollIntoView");
    const jump = entry!.querySelector<HTMLButtonElement>(".comment-gutter__jump");
    expect(jump).toBeTruthy();
    await fireEvent.click(jump!);
    expect(scrollSpy).toHaveBeenCalled();
  });

  it("(inline, default) commentLayout omitted — draft still renders as .rmd-thread-wrap", async () => {
    const stores = makeStores();
    stores.diff.setActivePR("local", "demo", 1);

    // Default (no commentLayout prop) must remain inline.
    const { container } = renderViewWithStores(stores);
    await settle();

    stores.diff.addDraftComment({
      path: "doc.md",
      line: 1,
      side: "RIGHT",
      commitSha: "abc",
      body: "inline check",
    });
    await settle();

    expect(container.querySelector(".rmd-thread-wrap")).toBeTruthy();
    // No gutter column in inline mode.
    expect(container.querySelector(".rmd-gutter-col")).toBeNull();
  });
});
