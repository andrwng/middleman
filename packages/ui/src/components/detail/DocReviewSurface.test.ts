import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/svelte";
import DocReviewSurface from "./DocReviewSurface.svelte";
import RenderedMarkdownView from "../diff/RenderedMarkdownView.svelte";
import { STORES_KEY, NAVIGATE_KEY } from "../../context.js";
import { createDiffStore } from "../../stores/diff.svelte.js";
import { createAIStore } from "../../stores/ai.svelte.js";
import { createDetailStore } from "../../stores/detail.svelte.js";
import { createReviewThreadsStore } from "../../stores/reviewThreads.svelte.js";
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

afterEach(() => {
  cleanup();
});

describe("DocReviewSurface", () => {
  it("renders a back affordance linking to the files review route", () => {
    renderSurface();
    // There should be a back button/link to the /files route.
    const back = screen.getByRole("button", { name: /review/i });
    expect(back).toBeTruthy();
  });

  it("clicking the back affordance navigates to the review route without basePath prefix", () => {
    const { navigateFn } = renderSurface();
    const back = screen.getByRole("button", { name: /review/i });
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
});
