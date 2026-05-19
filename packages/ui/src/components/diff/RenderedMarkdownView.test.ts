import { beforeEach, describe, it, expect, vi } from "vitest";
import { render } from "@testing-library/svelte";
import RenderedMarkdownView from "./RenderedMarkdownView.svelte";

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

describe("RenderedMarkdownView", () => {
  it("renders per-line anchor spans in the body", async () => {
    const { container } = render(RenderedMarkdownView, {
      owner: "local",
      name: "demo",
      number: 1,
      path: "doc.md",
      sha: "abc",
      hunks: [],
    });
    await new Promise((r) => setTimeout(r, 0));
    const anchors = container.querySelectorAll(".rmd-anchor");
    expect(anchors.length).toBeGreaterThanOrEqual(2);
    expect(anchors[0]?.getAttribute("data-anchor-line")).toBe("1");
    expect(anchors[1]?.getAttribute("data-anchor-line")).toBe("2");
  });
});
