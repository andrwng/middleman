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

function renderViewWithStores(stores: ReturnType<typeof makeStores>) {
  return render(RenderedMarkdownView, {
    props: {
      owner: "local",
      name: "demo",
      number: 1,
      path: "doc.md",
      sha: "abc",
      hunks: [],
    },
    context: new Map([[STORES_KEY, stores]]),
  });
}

function renderView() {
  return renderViewWithStores(makeStores());
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
});
