import { afterEach, describe, expect, it, vi } from "vitest";
import { createDiffStore } from "./diff.svelte.js";
import type { MiddlemanClient } from "../types.js";

function stubClient(): MiddlemanClient {
  return {
    GET: vi.fn(async () => ({ data: undefined, error: undefined })),
    POST: vi.fn(async () => ({ data: undefined, error: undefined })),
    DELETE: vi.fn(async () => ({ data: undefined, error: undefined })),
  } as unknown as MiddlemanClient;
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe("clearDraftCommentsForPath", () => {
  it("clearDraftCommentsForPath removes only that path's drafts", () => {
    const store = createDiffStore({ client: stubClient() });
    store.setActivePR("local", "demo", 1);
    store.addDraftComment({ path: "a.md", line: 1, side: "RIGHT", commitSha: "WORKING-TREE", body: "a1" });
    store.addDraftComment({ path: "a.md", line: 2, side: "RIGHT", commitSha: "WORKING-TREE", body: "a2" });
    store.addDraftComment({ path: "b.md", line: 1, side: "RIGHT", commitSha: "WORKING-TREE", body: "b1" });

    store.clearDraftCommentsForPath("a.md");

    expect(store.getDraftCommentsForPath("a.md")).toHaveLength(0);
    expect(store.getDraftCommentsForPath("b.md")).toHaveLength(1);
    expect(store.getDraft().comments).toHaveLength(1);
  });
});

describe("clearDiff", () => {
  it("clears a stale, unclaimed reveal target so it cannot survive into the next PR", () => {
    // revealTarget is keyed only by (path, line) with no PR/owner scope
    // of its own — the store is an app-wide singleton, reused as-is
    // across PRs. If a jump on PR #1 never resolved (e.g. the file
    // never mounted before the reviewer navigated away), the target
    // must not still be armed once DiffView unmounts and clearDiff()
    // runs, or an unrelated file in the next PR that happens to share
    // the same line number would spontaneously expand and flash.
    const store = createDiffStore({ client: stubClient() });
    store.setActivePR("acme", "widget", 1);
    store.requestRevealLine("src/foo.ts", 42);
    expect(store.getRevealTarget()).toEqual({ path: "src/foo.ts", line: 42 });

    store.clearDiff();

    expect(store.getRevealTarget()).toBeNull();
  });
});

describe("getCurrentCommitSha for a patchset-pair scope", () => {
  // loadPatchsets fetches over raw `fetch`, not client.GET (matching
  // loadCommits), so the patchsets list has to be seeded that way too.
  function installPatchsetsFetch(
    patchsets: Array<{
      id: number;
      number: number;
      head_sha: string;
      base_sha: string;
      merge_base_sha: string;
      observed_at: string;
    }>,
  ): void {
    globalThis.fetch = vi.fn(async (input: string | URL | Request) => {
      const url =
        typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
      if (url.includes("/patchsets")) {
        return new Response(JSON.stringify({ patchsets }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }
      return new Response(JSON.stringify({}), { status: 404 });
    }) as unknown as typeof fetch;
  }

  it("resolves to the target patchset's head SHA when toNumber is the latest patchset", async () => {
    installPatchsetsFetch([
      { id: 1, number: 1, head_sha: "sha-ps1", base_sha: "base", merge_base_sha: "base", observed_at: "2026-01-01T00:00:00Z" },
      { id: 2, number: 2, head_sha: "sha-ps2", base_sha: "sha-ps1", merge_base_sha: "base", observed_at: "2026-01-02T00:00:00Z" },
    ]);
    const store = createDiffStore({ client: stubClient() });
    store.setActivePR("acme", "widget", 1);
    await store.loadPatchsets();

    store.selectPatchsets(1, 2);

    expect(store.getCurrentCommitSha()).toBe("sha-ps2");
  });

  it("resolves to the earlier patchset's head SHA (not the PR head) when toNumber is not the latest patchset", async () => {
    // This is the case Fix 1 addresses: before the "patchsets" branch
    // existed, getCurrentCommitSha fell through to commits[0].sha (the
    // PR head) even though the rendered interdiff's new side is
    // patchset 2's head, not patchset 3's.
    installPatchsetsFetch([
      { id: 1, number: 1, head_sha: "sha-ps1", base_sha: "base", merge_base_sha: "base", observed_at: "2026-01-01T00:00:00Z" },
      { id: 2, number: 2, head_sha: "sha-ps2", base_sha: "sha-ps1", merge_base_sha: "base", observed_at: "2026-01-02T00:00:00Z" },
      { id: 3, number: 3, head_sha: "sha-ps3", base_sha: "sha-ps2", merge_base_sha: "base", observed_at: "2026-01-03T00:00:00Z" },
    ]);
    const store = createDiffStore({ client: stubClient() });
    store.setActivePR("acme", "widget", 1);
    await store.loadPatchsets();

    store.selectPatchsets(1, 2);

    expect(store.getCurrentCommitSha()).toBe("sha-ps2");
  });
});
