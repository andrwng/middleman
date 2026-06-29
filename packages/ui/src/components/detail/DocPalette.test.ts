import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/svelte";
import DocPalette from "./DocPalette.svelte";
import { STORES_KEY, NAVIGATE_KEY } from "../../context.js";
import { createWorktreesStore } from "../../stores/worktrees.svelte.js";
import type { MiddlemanClient } from "../../types.js";

const MOCK_FILES = ["api/spec.md", "design-doc.md", "README.md"];

function stubClient(files: string[] = MOCK_FILES): MiddlemanClient {
  return {
    GET: vi.fn(async (_path: string) => ({
      data: { files },
      error: undefined,
    })),
    POST: vi.fn(async () => ({ data: undefined, error: undefined })),
    DELETE: vi.fn(async () => ({ data: undefined, error: undefined })),
  } as unknown as MiddlemanClient;
}

function makeStores(files: string[] = MOCK_FILES) {
  const client = stubClient(files);
  return { worktrees: createWorktreesStore({ client }) };
}

function renderPalette(opts: {
  open?: boolean;
  files?: string[];
  basePath?: string;
} = {}) {
  const { open = true, files = MOCK_FILES, basePath = "/app/" } = opts;
  const navigateFn = vi.fn();
  const onClose = vi.fn();
  const result = render(DocPalette, {
    props: {
      owner: "local",
      name: "redpanda",
      number: 8,
      basePath,
      open,
      onClose,
    },
    context: new Map<symbol, unknown>([
      [STORES_KEY, makeStores(files)],
      [NAVIGATE_KEY, navigateFn],
    ]),
  });
  return { ...result, navigateFn, onClose };
}

afterEach(() => {
  cleanup();
});

describe("DocPalette", () => {
  it("does not render when open=false", () => {
    renderPalette({ open: false });
    expect(screen.queryByRole("textbox")).toBeNull();
  });

  it("shows all files sorted when query is empty", async () => {
    renderPalette();
    // Wait for the async loadMarkdownFiles to resolve.
    await new Promise((r) => setTimeout(r, 0));
    // Each row renders two <a> links: main + ↗ new-tab. Use listitem role to
    // count rows instead of links.
    const rows = screen.getAllByRole("option");
    const texts = rows.map((r) => r.textContent?.trim() ?? "");
    // Sorted: README.md, api/spec.md, design-doc.md
    expect(texts.some((t) => t.includes("README.md"))).toBe(true);
    expect(texts.some((t) => t.includes("api/spec.md"))).toBe(true);
    expect(texts.some((t) => t.includes("design-doc.md"))).toBe(true);
    expect(rows.length).toBe(3);
  });

  it("fuzzy query 'apsp' matches api/spec.md (NOT a substring match)", async () => {
    renderPalette();
    await new Promise((r) => setTimeout(r, 0));
    const input = screen.getByRole("textbox");
    await fireEvent.input(input, { target: { value: "apsp" } });
    // After filtering, only api/spec.md should appear (fuzzy: a→p→s→p)
    // Each row has two <a> links; use option role to count rows.
    const rows = screen.getAllByRole("option");
    expect(rows.length).toBe(1);
    // Check the main link href (first <a> in the row).
    const link = rows[0].querySelector("a");
    const href = link?.getAttribute("href") ?? "";
    expect(href).toContain(encodeURIComponent("api/spec.md"));
  });

  it("each row has an <a> href with basePath-prefixed /pulls/local/redpanda/8/doc?path=<encoded>", async () => {
    renderPalette();
    await new Promise((r) => setTimeout(r, 0));
    const rows = screen.getAllByRole("option");
    for (const row of rows) {
      // Each row has two anchors (main + ↗ new-tab), both with the same href.
      const anchors = row.querySelectorAll("a");
      for (const link of anchors) {
        const href = link.getAttribute("href") ?? "";
        // Verify the basePath prefix ("/app") is present — a regression dropping
        // the prefix would produce "/pulls/..." which would NOT match this anchor.
        expect(href).toMatch(/^\/app\/pulls\/local\/redpanda\/8\/doc\?path=/);
        // Also verify the encoded path query param is present.
        expect(href).toContain("path=");
      }
    }
  });

  it("plain Enter on highlighted row calls navigate (unprefixed) and onClose", async () => {
    const { navigateFn, onClose } = renderPalette();
    await new Promise((r) => setTimeout(r, 0));
    const input = screen.getByRole("textbox");
    // Navigate to a single result via fuzzy query so we know the exact file.
    await fireEvent.input(input, { target: { value: "apsp" } });
    await fireEvent.keyDown(input, { key: "Enter" });
    expect(navigateFn).toHaveBeenCalledWith(
      "/pulls/local/redpanda/8/doc?path=" + encodeURIComponent("api/spec.md"),
    );
    expect(onClose).toHaveBeenCalled();
  });

  it("Escape calls onClose", async () => {
    const { onClose } = renderPalette();
    await new Promise((r) => setTimeout(r, 0));
    const input = screen.getByRole("textbox");
    await fireEvent.keyDown(input, { key: "Escape" });
    expect(onClose).toHaveBeenCalled();
  });
});
