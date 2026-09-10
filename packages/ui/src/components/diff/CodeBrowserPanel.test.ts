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
  expandedDirs?: Set<string>;
  content?: string | null;
  status?: CodeBrowserStatus;
  error?: string | null;
}

function fakeCodeBrowserStore(overrides: FakeStoreOverrides = {}) {
  const {
    path = null,
    entriesByDir = new Map(),
    expandedDirs = new Set<string>(),
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
    get expandedDirs() {
      return expandedDirs;
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
    toggleDir: vi.fn(),
    listAllFiles: vi.fn(async () => []),
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
  forcePath?: boolean,
) {
  const codeBrowser = fakeCodeBrowserStore(overrides);
  const rendered = render(CodeBrowserPanel, {
    props: {
      owner: "acme",
      name: "widget",
      number: 1,
      sha: "deadbeef",
      initialPath: "src/a.txt",
      ...(forcePath !== undefined && { forcePath }),
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

  it("passes forcePath through to open() when set, to bypass a stale bookmark", () => {
    const { codeBrowser } = renderPanel({}, vi.fn(), true);
    expect(codeBrowser.open).toHaveBeenCalledWith(
      "acme",
      "widget",
      1,
      "deadbeef",
      "src/a.txt",
      { forcePath: true },
    );
  });

  it("renders a directory already marked expanded (via the store's expandedDirs)", async () => {
    const entriesByDir = new Map<string, TreeEntry[]>([
      ["", [dirEntry()]],
      ["src", [fileEntry()]],
    ]);
    renderPanel({ entriesByDir, expandedDirs: new Set(["src"]) });

    expect(await screen.findByText("a.txt")).toBeTruthy();
  });

  it("clicking a directory delegates expand/collapse to the store's toggleDir", async () => {
    const entriesByDir = new Map<string, TreeEntry[]>([["", [dirEntry()]]]);
    const { codeBrowser } = renderPanel({ entriesByDir });

    await fireEvent.click(screen.getByText("src"));

    expect(codeBrowser.toggleDir).toHaveBeenCalledWith("src");
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

  it("renders a 1-indexed line number beside each line", async () => {
    renderPanel({ status: "ready", content: "one\ntwo\nthree" });
    await screen.findByText("one");

    const nums = Array.from(document.querySelectorAll(".code-browser-line-num")).map(
      (el) => el.textContent,
    );
    expect(nums).toEqual(["1", "2", "3"]);
  });

  it("shows a loading state while a file is loading", () => {
    renderPanel({ status: "loading" });
    expect(screen.getByText(/loading/i)).toBeTruthy();
  });

  it("shows a missing-file message when the file doesn't exist at this commit", () => {
    renderPanel({ status: "missing" });
    expect(screen.getByText(/doesn't exist/i)).toBeTruthy();
  });

  // Finding 2: a truncated /blob response (file over the size cap) must
  // render a distinct "too large" state, not a blank pane.
  it("shows a too-large message when the file was truncated", () => {
    renderPanel({ status: "truncated" });
    expect(screen.getByText(/too large/i)).toBeTruthy();
  });

  // Finding 3: status "error" must be reachable and render distinctly
  // from "missing" -- before this fix it was dead code in the template.
  it("shows the store's error message when status is error, distinct from missing", () => {
    renderPanel({ status: "error", error: "read blob: exit status 128" });
    expect(screen.getByText("read blob: exit status 128")).toBeTruthy();
    expect(screen.queryByText(/doesn't exist/i)).toBeNull();
  });

  // Finding 5 (stale-flash on reopen): the panel's own Close control must
  // reset the store, not just tell the parent to unmount it -- otherwise
  // the store's state (content/path/status) survives across a close and
  // the next mount briefly shows the previous file before its own load
  // resolves.
  it("resets the store via close() when the close control is activated", async () => {
    const onclose = vi.fn();
    const { codeBrowser } = renderPanel({}, onclose);
    await fireEvent.click(screen.getByRole("button", { name: /close/i }));
    expect(codeBrowser.close).toHaveBeenCalled();
    expect(onclose).toHaveBeenCalled();
  });

  // Finding 1: initialPath must be consulted once per mount, not on every
  // reactive change -- j/k navigation elsewhere in the diff must not
  // silently re-seed an already-open panel to a different file. sha IS
  // still reactive (stepping a commit re-fetches the same sticky path).
  it("does not re-open when only initialPath changes, but does re-open when sha changes", async () => {
    const { codeBrowser, rerender } = renderPanel();
    expect(codeBrowser.open).toHaveBeenCalledTimes(1);
    expect(codeBrowser.open).toHaveBeenLastCalledWith(
      "acme",
      "widget",
      1,
      "deadbeef",
      "src/a.txt",
    );

    await rerender({
      owner: "acme",
      name: "widget",
      number: 1,
      sha: "deadbeef",
      initialPath: "src/other.txt",
      onclose: vi.fn(),
    });
    expect(codeBrowser.open).toHaveBeenCalledTimes(1);

    await rerender({
      owner: "acme",
      name: "widget",
      number: 1,
      sha: "cafef00d",
      initialPath: "src/other.txt",
      onclose: vi.fn(),
    });
    expect(codeBrowser.open).toHaveBeenCalledTimes(2);
    // Once sha legitimately triggers a re-run, it reads initialPath's
    // current value (src/other.txt) -- untrack() only stops a prop change
    // from being what TRIGGERS the re-run by itself; it doesn't freeze the
    // value at mount. In DiffView's real usage this prop only changes
    // together with a deliberate reopen (the `b` key, the toolbar button,
    // or a symbol-refs browse action), never as a side effect of j/k.
    expect(codeBrowser.open).toHaveBeenLastCalledWith(
      "acme",
      "widget",
      1,
      "cafef00d",
      "src/other.txt",
    );
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

  it("scrolls to and flashes the requested line once its path matches the open file, then reports it handled", async () => {
    const scrollIntoView = vi.fn();
    HTMLElement.prototype.scrollIntoView = scrollIntoView;
    const onRevealed = vi.fn();
    const codeBrowser = fakeCodeBrowserStore({
      path: "src/a.txt",
      status: "ready",
      content: "one\ntwo\nthree",
    });
    render(CodeBrowserPanel, {
      props: {
        owner: "acme",
        name: "widget",
        number: 1,
        sha: "deadbeef",
        initialPath: "src/a.txt",
        reveal: { path: "src/a.txt", line: 2, nonce: 1 },
        onRevealed,
        onclose: vi.fn(),
      },
      context: new Map<symbol, unknown>([[STORES_KEY, { codeBrowser }]]),
    });

    await screen.findByText("two");
    await new Promise((r) => setTimeout(r, 0)); // let the reveal effect's tick() resolve

    expect(scrollIntoView).toHaveBeenCalled();
    expect(onRevealed).toHaveBeenCalled();
    const line2 = document.querySelector('[data-line="2"]');
    expect(line2?.className).toContain("code-browser-line-row--flash");
  });

  it("does not re-handle a reveal whose target path doesn't match the currently open file", async () => {
    const scrollIntoView = vi.fn();
    HTMLElement.prototype.scrollIntoView = scrollIntoView;
    const codeBrowser = fakeCodeBrowserStore({
      path: "src/other.txt",
      status: "ready",
      content: "one\ntwo",
    });
    render(CodeBrowserPanel, {
      props: {
        owner: "acme",
        name: "widget",
        number: 1,
        sha: "deadbeef",
        initialPath: "src/a.txt",
        reveal: { path: "src/a.txt", line: 2, nonce: 1 },
        onclose: vi.fn(),
      },
      context: new Map<symbol, unknown>([[STORES_KEY, { codeBrowser }]]),
    });

    await screen.findByText("two");
    await new Promise((r) => setTimeout(r, 0));

    expect(scrollIntoView).not.toHaveBeenCalled();
  });

  it("opens the Go to file palette on 't' and navigates to the selected file", async () => {
    const codeBrowser = fakeCodeBrowserStore();
    codeBrowser.listAllFiles.mockResolvedValue(["README.md", "internal/handler.go"]);
    render(CodeBrowserPanel, {
      props: {
        owner: "acme",
        name: "widget",
        number: 1,
        sha: "deadbeef",
        initialPath: "src/a.txt",
        onclose: vi.fn(),
      },
      context: new Map<symbol, unknown>([[STORES_KEY, { codeBrowser }]]),
    });

    // The keydown listener is scoped to the panel root, not the window --
    // it must not compete with DiffView's global j/k/[/]/m/s/b handling.
    const panel = screen.getByText("Browse files").closest(".code-browser-panel")!;
    await fireEvent.keyDown(panel, { key: "t" });

    const option = await screen.findByText("internal/handler.go");
    await fireEvent.click(option);

    expect(codeBrowser.navigateTo).toHaveBeenCalledWith("internal/handler.go");
  });

  it("opens the Go to file palette on '/' too", async () => {
    const codeBrowser = fakeCodeBrowserStore();
    codeBrowser.listAllFiles.mockResolvedValue(["README.md"]);
    render(CodeBrowserPanel, {
      props: {
        owner: "acme",
        name: "widget",
        number: 1,
        sha: "deadbeef",
        initialPath: "src/a.txt",
        onclose: vi.fn(),
      },
      context: new Map<symbol, unknown>([[STORES_KEY, { codeBrowser }]]),
    });

    const panel = screen.getByText("Browse files").closest(".code-browser-panel")!;
    await fireEvent.keyDown(panel, { key: "/" });

    expect(await screen.findByPlaceholderText("Go to file…")).toBeTruthy();
  });

  // Regression: opening the panel doesn't focus anything inside it (no
  // input auto-focuses), so a listener scoped to the panel's own root
  // element only fires once focus/the event target already happens to be
  // somewhere inside that subtree. Firing on document.body -- where
  // focus actually sits after opening the panel via the `b` hotkey or the
  // toolbar button, with nothing inside the panel ever clicked -- is what
  // exposed this; the listener must be window-scoped to see it.
  it("opens the palette on '/' even when nothing inside the panel has focus", async () => {
    const codeBrowser = fakeCodeBrowserStore();
    codeBrowser.listAllFiles.mockResolvedValue(["README.md"]);
    render(CodeBrowserPanel, {
      props: {
        owner: "acme",
        name: "widget",
        number: 1,
        sha: "deadbeef",
        initialPath: "src/a.txt",
        onclose: vi.fn(),
      },
      context: new Map<symbol, unknown>([[STORES_KEY, { codeBrowser }]]),
    });

    await fireEvent.keyDown(document.body, { key: "/" });

    expect(await screen.findByPlaceholderText("Go to file…")).toBeTruthy();
  });

  it("filters the Go to file palette by fuzzy query", async () => {
    const codeBrowser = fakeCodeBrowserStore();
    codeBrowser.listAllFiles.mockResolvedValue(["README.md", "internal/handler.go"]);
    render(CodeBrowserPanel, {
      props: {
        owner: "acme",
        name: "widget",
        number: 1,
        sha: "deadbeef",
        initialPath: "src/a.txt",
        onclose: vi.fn(),
      },
      context: new Map<symbol, unknown>([[STORES_KEY, { codeBrowser }]]),
    });
    const panel = screen.getByText("Browse files").closest(".code-browser-panel")!;
    await fireEvent.keyDown(panel, { key: "t" });
    await screen.findByText("internal/handler.go");

    const input = screen.getByPlaceholderText("Go to file…");
    await fireEvent.input(input, { target: { value: "hdlr" } });

    expect(screen.queryByText("README.md")).toBeNull();
    expect(screen.getByText("internal/handler.go")).toBeTruthy();
  });
});
