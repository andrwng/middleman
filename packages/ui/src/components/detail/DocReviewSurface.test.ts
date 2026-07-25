import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/svelte";
import DocReviewSurface from "./DocReviewSurface.svelte";
import RenderedMarkdownView from "../diff/RenderedMarkdownView.svelte";
import { STORES_KEY, NAVIGATE_KEY } from "../../context.js";
import { createDiffStore } from "../../stores/diff.svelte.js";
import { createAIStore } from "../../stores/ai.svelte.js";
import { createDetailStore } from "../../stores/detail.svelte.js";
import { createReviewThreadsStore } from "../../stores/reviewThreads.svelte.js";
import { WORKING_TREE_SENTINEL } from "../../utils/worktreeSentinel.js";
import type { MiddlemanClient } from "../../types.js";

// RenderedMarkdownView fetches the blob inline; stub it out.
vi.mock("../diff/RenderedMarkdownView.svelte", () => ({
  default: vi.fn().mockImplementation(() => ({
    $$: {},
  })),
}));

function stubClient(): MiddlemanClient {
  return {
    GET: vi.fn(async () => ({ data: undefined, error: undefined })),
    POST: vi.fn(async () => ({ data: undefined, error: undefined })),
    DELETE: vi.fn(async () => ({ data: undefined, error: undefined })),
  } as unknown as MiddlemanClient;
}

function makeStores() {
  const client = stubClient();
  const diff = createDiffStore({ client });
  return {
    diff,
    ai: createAIStore(),
    detail: createDetailStore({ client: null as unknown as MiddlemanClient }),
    reviewThreads: createReviewThreadsStore({ client }),
  };
}

function renderSurface(docPath = "docs/README.md") {
  const navigateFn = vi.fn();
  const stores = makeStores();
  const result = render(DocReviewSurface, {
    props: {
      owner: "local",
      name: "demo",
      number: 42,
      path: docPath,
      basePath: "/",
    },
    context: new Map<symbol, unknown>([
      [STORES_KEY, stores],
      [NAVIGATE_KEY, navigateFn],
    ]),
  });
  return { ...result, navigateFn, stores };
}

beforeEach(() => {
  localStorage.clear();
});

afterEach(() => {
  cleanup();
});

describe("DocReviewSurface", () => {
  it("renders a back affordance linking to the files review route", () => {
    renderSurface();
    // There should be a back button/link to the /files route. Matched by
    // exact name: the new per-doc "Review (N)" button also contains
    // "review" and would otherwise collide with a /review/i regex match.
    const back = screen.getByRole("button", { name: "← Review" });
    expect(back).toBeTruthy();
  });

  it("clicking the back affordance navigates to the review route without basePath prefix", () => {
    const { navigateFn } = renderSurface();
    const back = screen.getByRole("button", { name: "← Review" });
    back.click();
    expect(navigateFn).toHaveBeenCalledWith(
      "/pulls/local/demo/42/files",
    );
  });

  it("renders an open-in-new-tab anchor whose href contains /doc?path= and the encoded path", () => {
    renderSurface("docs/README.md");
    const link = screen.getByRole("link");
    const href = link.getAttribute("href") ?? "";
    expect(href).toContain("/doc?path=");
    expect(href).toContain(encodeURIComponent("docs/README.md"));
  });

  it("renders the doc path", () => {
    const { container } = renderSurface("docs/README.md");
    expect(container.textContent).toContain("docs/README.md");
  });

  it("new-tab link includes basePath when basePath is /myapp/", () => {
    const navigateFn = vi.fn();
    render(DocReviewSurface, {
      props: {
        owner: "local",
        name: "demo",
        number: 42,
        path: "docs/guide.md",
        basePath: "/myapp/",
      },
      context: new Map<symbol, unknown>([
        [STORES_KEY, makeStores()],
        [NAVIGATE_KEY, navigateFn],
      ]),
    });
    const link = screen.getByRole("link");
    const href = link.getAttribute("href") ?? "";
    expect(href).toContain("/myapp/pulls/");
    expect(href).toContain(encodeURIComponent("docs/guide.md"));
  });

  it("passes commentLayout=\"gutter\" to RenderedMarkdownView", () => {
    renderSurface();
    const mock = vi.mocked(RenderedMarkdownView);
    // Svelte 5 calls the component function as Component(anchor, props).
    // The second argument is the props object.
    const props = mock.mock.calls[0]?.[1] as Record<string, unknown>;
    expect(props).toBeDefined();
    expect(props["commentLayout"]).toBe("gutter");
  });

  it("sets the diff store active PR on mount and resets it on unmount", () => {
    const { stores } = renderSurface();
    vi.spyOn(stores.diff, "setActivePR");

    // At this point the component has already mounted; spy was attached after.
    // Re-render with the spy in place so we can verify mount calls.
    cleanup();

    const navigateFn = vi.fn();
    const stores2 = makeStores();
    vi.spyOn(stores2.diff, "setActivePR");

    render(DocReviewSurface, {
      props: {
        owner: "local",
        name: "demo",
        number: 42,
        path: "docs/README.md",
        basePath: "/",
      },
      context: new Map<symbol, unknown>([
        [STORES_KEY, stores2],
        [NAVIGATE_KEY, navigateFn],
      ]),
    });

    expect(stores2.diff.setActivePR).toHaveBeenCalledWith("local", "demo", 42);

    cleanup();

    expect(stores2.diff.setActivePR).toHaveBeenLastCalledWith("", "", 0);
  });

  it("disables the Review button when there are no pending comments on this doc", () => {
    renderSurface("docs/README.md");
    const btn = screen.getByRole("button", { name: "Review (0)" }) as HTMLButtonElement;
    expect(btn).toBeTruthy();
    expect(btn.disabled).toBe(true);
  });

  it("shows a Review button with the per-doc pending count and opens the scoped panel", async () => {
    const docPath = "docs/README.md";
    const stores = makeStores();
    // Seed a real draft comment on this doc, on the same store instance
    // the component will use, before mounting: the draft key is derived
    // from owner/name/number, so setActivePR must be called first with
    // the same identity the component mounts with.
    stores.diff.setActivePR("local", "demo", 42);
    stores.diff.addDraftComment({
      path: docPath,
      line: 1,
      side: "RIGHT",
      commitSha: WORKING_TREE_SENTINEL,
      body: "x",
    });

    const navigateFn = vi.fn();
    render(DocReviewSurface, {
      props: {
        owner: "local",
        name: "demo",
        number: 42,
        path: docPath,
        basePath: "/",
      },
      context: new Map<symbol, unknown>([
        [STORES_KEY, stores],
        [NAVIGATE_KEY, navigateFn],
      ]),
    });

    const btn = screen.getByRole("button", { name: "Review (1)" }) as HTMLButtonElement;
    expect(btn).toBeTruthy();
    expect(btn.disabled).toBe(false);
    expect(screen.queryByRole("dialog", { name: "Finish review" })).toBeNull();

    await fireEvent.click(btn);

    expect(screen.getByRole("dialog", { name: "Finish review" })).toBeTruthy();
  });
});
