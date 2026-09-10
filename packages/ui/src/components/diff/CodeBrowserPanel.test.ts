import { cleanup, fireEvent, render, screen } from "@testing-library/svelte";
import { afterEach, describe, expect, it, vi } from "vitest";
import { STORES_KEY } from "../../context.js";
import type { CodeBrowserStatus, TreeEntry } from "../../stores/codeBrowser.svelte.js";

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
    expect(await screen.findByText("const")).toBeTruthy();
  });
});
