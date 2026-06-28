import { beforeEach, describe, it, expect, vi } from "vitest";
import { render, fireEvent } from "@testing-library/svelte";
import RenderedMarkdownView from "./RenderedMarkdownView.svelte";
import { STORES_KEY } from "../../context.js";
import { createDiffStore } from "../../stores/diff.svelte.js";
import { createAIStore } from "../../stores/ai.svelte.js";
import { createDetailStore } from "../../stores/detail.svelte.js";
import type { MiddlemanClient } from "../../types.js";

function stubClient(): MiddlemanClient {
  return {
    GET: vi.fn(async () => ({ data: undefined, error: undefined })),
    POST: vi.fn(async () => ({ data: undefined, error: undefined })),
    DELETE: vi.fn(async () => ({ data: undefined, error: undefined })),
  } as unknown as MiddlemanClient;
}

beforeEach(() => {
  globalThis.fetch = vi.fn(async () => ({
    ok: true,
    status: 200,
    json: async () => ({
      content: "Line one.\nLine two.\n",
      truncated: false,
    }),
  }) as unknown as Response);

  // jsdom does not implement scrollIntoView; stub it so that composer
  // components that call textarea.scrollIntoView() in a $effect don't throw.
  Element.prototype.scrollIntoView = vi.fn();
});

function renderView() {
  return render(RenderedMarkdownView, {
    props: {
      owner: "local",
      name: "demo",
      number: 1,
      path: "doc.md",
      sha: "abc",
      hunks: [],
    },
    context: new Map([[
      STORES_KEY,
      {
        diff: createDiffStore({ client: stubClient() }),
        ai: createAIStore(),
        detail: createDetailStore({ client: null as unknown as MiddlemanClient }),
      },
    ]]),
  });
}

describe("RenderedMarkdownView", () => {
  it("renders per-line anchor spans in the body", async () => {
    const { container } = renderView();
    await new Promise((r) => setTimeout(r, 0));
    const anchors = container.querySelectorAll(".rmd-anchor");
    expect(anchors.length).toBeGreaterThanOrEqual(2);
    expect(anchors[0]?.getAttribute("data-anchor-line")).toBe("1");
    expect(anchors[1]?.getAttribute("data-anchor-line")).toBe("2");
  });

  it("composer wrap carries a data-rmd-block-idx attribute matching the clicked block", async () => {
    const { container } = renderView();
    // Wait for fetch + DOM effects to settle.
    await new Promise((r) => setTimeout(r, 0));

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
    await new Promise((r) => setTimeout(r, 0));

    const buttons = container.querySelectorAll(".rmd-ask-ai-btn");
    expect(buttons.length).toBeGreaterThanOrEqual(1);

    const firstBtn = buttons[0] as HTMLElement;
    await fireEvent.click(firstBtn);

    const wrap = container.querySelector(".rmd-composer-wrap");
    expect(wrap).toBeTruthy();
    expect(wrap?.getAttribute("data-rmd-block-idx")).toBe("0");
  });
});
