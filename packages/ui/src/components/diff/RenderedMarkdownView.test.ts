import { beforeEach, describe, it, expect, vi } from "vitest";
import { render } from "@testing-library/svelte";
import RenderedMarkdownView from "./RenderedMarkdownView.svelte";
import { STORES_KEY } from "../../context.js";
import { createDiffStore } from "../../stores/diff.svelte.js";
import { createAIStore } from "../../stores/ai.svelte.js";
import { createDetailStore } from "../../stores/detail.svelte.js";
import type { MiddlemanClient } from "../../types.js";

beforeEach(() => {
  globalThis.fetch = vi.fn(async () => ({
    ok: true,
    status: 200,
    json: async () => ({
      content: "Line one.\nLine two.\n",
      truncated: false,
    }),
  }) as unknown as Response);
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
        diff: createDiffStore(),
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
});
