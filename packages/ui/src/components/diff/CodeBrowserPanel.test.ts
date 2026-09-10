import { cleanup, fireEvent, render, screen } from "@testing-library/svelte";
import { afterEach, describe, expect, it, vi } from "vitest";
import { STORES_KEY } from "../../context.js";
import type { CodeBrowserStatus, TreeEntry } from "../../stores/codeBrowser.svelte.js";

// Mock highlight utils to avoid loading real Shiki (WASM + grammar) in
// tests, matching the convention established in DiffFile.test.ts. Returns
// deterministic dual-theme tokens for the one line this suite highlights,
// so both the token-boundary assertion and the dark/light color-wiring
// assertion below are exercised against fixed data rather than Shiki's
// actual keyword tokenization.
vi.mock("../../utils/highlight.js", () => ({
  langFromPath: (path: string) => (path.endsWith(".ts") ? "typescript" : undefined),
  tokenizeLineDual: (code: string) =>
    Promise.resolve(
      code === "const x = 1;"
        ? [
            { content: "const", darkColor: "#ff7b72", lightColor: "#cf222e" },
            { content: " x = 1;" },
          ]
        : [{ content: code }],
    ),
}));

import CodeBrowserPanel from "./CodeBrowserPanel.svelte";

interface FakeStoreOverrides {
  path?: string | null;
  entriesByDir?: Map<string, TreeEntry[]>;
  content?: string | null;
  status?: CodeBrowserStatus;
  error?: string | null;
}

function fakeCodeBrowserStore(overrides: FakeStoreOverrides = {}) {
  const {
    path = null,
    entriesByDir = new Map(),
    content = null,
    status = "ready",
    error = null,
  } = overrides;
  return {
    get isOpen() {
      return true;
    },
    get path() {
      return path;
    },
    get entriesByDir() {
      return entriesByDir;
    },
    get content() {
      return content;
    },
    get status() {
      return status;
    },
    get error() {
      return error;
    },
    open: vi.fn(async () => {}),
    close: vi.fn(),
    navigateTo: vi.fn(async () => {}),
    loadTree: vi.fn(async () => {}),
  };
}

function dirEntry(over: Partial<TreeEntry> = {}): TreeEntry {
  return { name: "src", path: "src", type: "dir", ...over };
}

function fileEntry(over: Partial<TreeEntry> = {}): TreeEntry {
  return { name: "a.txt", path: "src/a.txt", type: "file", ...over };
}

function renderPanel(
  overrides: FakeStoreOverrides = {},
  onclose: () => void = vi.fn(),
) {
  const codeBrowser = fakeCodeBrowserStore(overrides);
  const rendered = render(CodeBrowserPanel, {
    props: {
      owner: "acme",
      name: "widget",
      number: 1,
      sha: "deadbeef",
      initialPath: "src/a.txt",
      onclose,
    },
    context: new Map<symbol, unknown>([[STORES_KEY, { codeBrowser }]]),
  });
  return { ...rendered, codeBrowser };
}

afterEach(() => {
  cleanup();
});

describe("CodeBrowserPanel", () => {
  it("renders the root tree entries", async () => {
    const entriesByDir = new Map<string, TreeEntry[]>([["", [dirEntry()]]]);
    renderPanel({ entriesByDir });
    expect(await screen.findByText("src")).toBeTruthy();
  });

  it("calls onclose when the close control is activated", async () => {
    const onclose = vi.fn();
    renderPanel({}, onclose);
    await fireEvent.click(screen.getByRole("button", { name: /close/i }));
    expect(onclose).toHaveBeenCalled();
  });

  it("calls open with the owner/name/number/sha/initialPath on mount", () => {
    const { codeBrowser } = renderPanel();
    expect(codeBrowser.open).toHaveBeenCalledWith(
      "acme",
      "widget",
      1,
      "deadbeef",
      "src/a.txt",
    );
  });

  it("expands a directory without refetching when its children are already loaded", async () => {
    const entriesByDir = new Map<string, TreeEntry[]>([
      ["", [dirEntry()]],
      ["src", [fileEntry()]],
    ]);
    const { codeBrowser } = renderPanel({ entriesByDir });

    await fireEvent.click(screen.getByText("src"));

    expect(await screen.findByText("a.txt")).toBeTruthy();
    expect(codeBrowser.loadTree).not.toHaveBeenCalled();
  });

  it("lazily loads a directory's children the first time it is expanded", async () => {
    const entriesByDir = new Map<string, TreeEntry[]>([["", [dirEntry()]]]);
    const { codeBrowser } = renderPanel({ entriesByDir });

    await fireEvent.click(screen.getByText("src"));

    expect(codeBrowser.loadTree).toHaveBeenCalledWith("src");
  });

  it("navigates to a file when a file entry is clicked", async () => {
    const entriesByDir = new Map<string, TreeEntry[]>([["", [fileEntry({ path: "a.txt", name: "a.txt" })]]]);
    const { codeBrowser } = renderPanel({ entriesByDir });

    await fireEvent.click(screen.getByText("a.txt"));

    expect(codeBrowser.navigateTo).toHaveBeenCalledWith("a.txt");
  });

  it("renders the file content as plain text", async () => {
    renderPanel({ status: "ready", content: "hello world" });
    expect(await screen.findByText("hello world")).toBeTruthy();
  });

  it("shows a loading state while a file is loading", () => {
    renderPanel({ status: "loading" });
    expect(screen.getByText(/loading/i)).toBeTruthy();
  });

  it("shows a missing-file message when the file doesn't exist at this commit", () => {
    renderPanel({ status: "missing" });
    expect(screen.getByText(/doesn't exist/i)).toBeTruthy();
  });

  it("renders highlighted tokens instead of raw <pre> text for a known language", async () => {
    renderPanel({ path: "src/a.ts", status: "ready", content: "const x = 1;" });
    const token = await screen.findByText("const");
    expect(token.tagName).toBe("SPAN");
    // Verify the dual-theme wiring itself, not just that tokenization
    // split the line: the span must carry both the dark and light color
    // custom properties DiffLine.svelte's CSS switches on.
    expect((token as HTMLElement).style.getPropertyValue("--dc")).toBe("#ff7b72");
    expect((token as HTMLElement).style.getPropertyValue("--lc")).toBe("#cf222e");
  });
});
