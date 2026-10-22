import { afterEach, describe, expect, it, vi } from "vitest";
import { createReviewDraftsApi, DraftApiError } from "./reviewDraftsApi.js";

const pr = { owner: "acme", name: "wid get", number: 7 };

function mockFetch(status: number, body: unknown) {
  const fn = vi.fn(async () =>
    new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } }),
  );
  globalThis.fetch = fn as unknown as typeof fetch;
  return fn;
}

afterEach(() => vi.restoreAllMocks());

describe("reviewDraftsApi", () => {
  it("lists drafts at the encoded PR path", async () => {
    const fn = mockFetch(200, { drafts: [], meta: { body: "", event: "" } });
    const api = createReviewDraftsApi(() => "/");
    await api.list(pr);
    expect(fn).toHaveBeenCalledWith("/api/v1/repos/acme/wid%20get/pulls/7/review-drafts", expect.anything());
  });

  it("sends deletes as a bulk POST with a JSON content type", async () => {
    const fn = mockFetch(200, { deleted: 2 });
    const api = createReviewDraftsApi(() => "/");
    await api.remove(pr, ["a", "b"]);
    const [url, init] = fn.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/v1/repos/acme/wid%20get/pulls/7/review-drafts/delete");
    expect(init.method).toBe("POST");
    expect((init.headers as Record<string, string>)["content-type"]).toBe("application/json");
    expect(JSON.parse(init.body as string)).toEqual({ ids: ["a", "b"] });
  });

  it("throws DraftApiError with server detail and status", async () => {
    mockFetch(409, { detail: "could not be resolved" });
    const api = createReviewDraftsApi(() => "/");
    const err = await api.publish(pr, { event: "COMMENT", body: "" }).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(DraftApiError);
    expect((err as DraftApiError).status).toBe(409);
    expect((err as DraftApiError).message).toBe("could not be resolved");
  });

  it("skips the network for empty deletes", async () => {
    const fn = mockFetch(200, {});
    const api = createReviewDraftsApi(() => "/");
    await api.remove(pr, []);
    expect(fn).not.toHaveBeenCalled();
  });

  it("unwraps the summary list", async () => {
    mockFetch(200, { prs: [{ owner: "acme", name: "widget", number: 1, count: 2 }] });
    const api = createReviewDraftsApi(() => "/base/");
    expect(await api.summary()).toEqual([{ owner: "acme", name: "widget", number: 1, count: 2 }]);
  });

  it("posts transitions to the draft action routes", async () => {
    const fn = mockFetch(200, { id: "s1", state: "draft" });
    const api = createReviewDraftsApi(() => "/");
    await api.accept(pr, "s 1");
    const [url, init] = fn.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/v1/repos/acme/wid%20get/pulls/7/review-drafts/s%201/accept");
    expect(init.method).toBe("POST");
    expect((init.headers as Record<string, string>)["content-type"]).toBe("application/json");
  });

  it("returns null when there is no review run yet", async () => {
    mockFetch(200, { run: null });
    const api = createReviewDraftsApi(() => "/");
    expect(await api.latestRun(pr)).toBeNull();
  });
});
