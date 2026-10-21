import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createDiffStore } from "./diff.svelte.js";
import type { MiddlemanClient } from "../types.js";
import type { DiffFile } from "../api/types.js";

const GLOBS_PATH = "/repos/{owner}/{name}/pulls/{number}/diff-collapse-globs";
const HUNKS_PATH = "/repos/{owner}/{name}/pulls/{number}/diff/hunks";

function file(
  path: string,
  opts: Partial<DiffFile> = {},
): DiffFile {
  return {
    path,
    old_path: path,
    status: "modified",
    is_binary: false,
    is_whitespace_only: false,
    additions: 1,
    deletions: 1,
    hunks: [],
    ...opts,
  };
}

function withHunk(f: DiffFile): DiffFile {
  return {
    ...f,
    hunks: [
      {
        old_start: 1,
        old_count: 1,
        new_start: 1,
        new_count: 1,
        lines: [{ type: "add", content: "x", new_num: 1 }],
      },
    ],
  };
}

function summary(files: DiffFile[]) {
  return {
    stale: false,
    whitespace_only_count: 0,
    diff_from: "base1",
    diff_to: "head1",
    files,
  };
}

// A summary whose total exceeds the 5000-line lazy threshold.
function largeSummaryFiles(): DiffFile[] {
  return [
    file("big/a.ts", { additions: 1000, deletions: 0 }),
    file("big/b.ts", { additions: 1000, deletions: 0 }),
    file("big/c.ts", { additions: 1000, deletions: 0 }),
    file("big/d.ts", { additions: 1000, deletions: 0 }),
    file("big/e.ts", { additions: 1000, deletions: 0 }),
    file("big/new.ts", {
      old_path: "big/old.ts",
      status: "renamed",
      additions: 100,
      deletions: 0,
    }),
  ];
}

interface FetchLog {
  urls: string[];
}

function installFetch(opts: {
  files: DiffFile[];
  diffFiles?: DiffFile[];
}): FetchLog {
  const log: FetchLog = { urls: [] };
  globalThis.fetch = vi.fn(async (input: string | URL | Request) => {
    const url =
      typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
    log.urls.push(url);
    if (/\/pulls\/\d+\/files(\?|$)/.test(url)) {
      return Response.json(summary(opts.files));
    }
    if (/\/pulls\/\d+\/diff(\?|$)/.test(url)) {
      return Response.json({
        stale: false,
        whitespace_only_count: 0,
        files: (opts.diffFiles ?? opts.files).map(withHunk),
      });
    }
    if (url.includes("/commits")) {
      return Response.json({
        commits: [
          { sha: "c1", message: "m", author_name: "a", authored_at: "2026-01-01T00:00:00Z" },
        ],
      });
    }
    return Response.json({}, { status: 404 });
  }) as unknown as typeof fetch;
  return log;
}

type HunksHandler = (body: Record<string, unknown>) => {
  data?: unknown;
  error?: unknown;
  response: { status: number };
};

function makeClient(opts: {
  globs?: string[];
  hunks?: HunksHandler;
} = {}) {
  const GET = vi.fn(async (path: string) => {
    if (path === GLOBS_PATH) {
      return { data: { globs: opts.globs ?? [] }, error: undefined, response: { status: 200 } };
    }
    return { data: undefined, error: undefined, response: { status: 200 } };
  });
  const PUT = vi.fn(async (_path: string, init: { body: { globs: string[] } }) => ({
    data: { globs: init.body.globs },
    error: undefined,
    response: { status: 200 },
  }));
  const POST = vi.fn(async (path: string, init: { body: Record<string, unknown> }) => {
    if (path === HUNKS_PATH && opts.hunks) return opts.hunks(init.body);
    return { data: undefined, error: undefined, response: { status: 200 } };
  });
  const client = { GET, PUT, POST, DELETE: vi.fn() } as unknown as MiddlemanClient;
  return { client, GET, PUT, POST };
}

function hunksFrom(files: DiffFile[]): HunksHandler {
  return (body) => {
    const paths = body.paths as string[];
    return {
      data: { files: files.filter((f) => paths.includes(f.path)).map(withHunk) },
      error: undefined,
      response: { status: 200 },
    };
  };
}

function isDiffUrl(u: string): boolean {
  return /\/pulls\/\d+\/diff(\?|$)/.test(u);
}

beforeEach(() => {
  localStorage.clear();
});

afterEach(() => {
  vi.restoreAllMocks();
  localStorage.clear();
});

describe("diff store lazy loading", () => {
  it("small diff makes one /files call, one /diff call, and no hunks call", async () => {
    const log = installFetch({ files: [file("a.ts"), file("b.ts")] });
    const { client, POST } = makeClient();
    const store = createDiffStore({ client });

    await store.loadDiff("acme", "widget", 1);

    expect(log.urls.filter((u) => u.includes("/files"))).toHaveLength(1);
    expect(log.urls.filter(isDiffUrl)).toHaveLength(1);
    expect(POST).not.toHaveBeenCalled();
    expect(store.isLazy()).toBe(false);
    expect(store.getDiff()?.files[0]?.hunks).toHaveLength(1);
    expect(store.getFileHunkState("a.ts")).toBe("loaded");
    store.requestHunks("a.ts");
    await new Promise((r) => setTimeout(r, 20));
    expect(POST).not.toHaveBeenCalled();
  });

  it("passes the scope and whitespace params to /files", async () => {
    const log = installFetch({ files: [file("a.ts")] });
    const { client } = makeClient();
    localStorage.setItem("diff-hide-whitespace", "true");
    const store = createDiffStore({ client });
    store.setActivePR("acme", "widget", 1);
    await store.loadCommits();
    await store.selectCommit("c1");

    const filesUrl = log.urls.filter((u) => u.includes("/files")).at(-1)!;
    expect(filesUrl).toContain("commit=c1");
    expect(filesUrl).toContain("whitespace=hide");
  });

  it("large summary skips /diff and loads hunks on request", async () => {
    const files = largeSummaryFiles();
    const log = installFetch({ files });
    const { client, POST } = makeClient({ hunks: hunksFrom(files) });
    const store = createDiffStore({ client });

    await store.loadDiff("acme", "widget", 1);

    expect(log.urls.filter(isDiffUrl)).toHaveLength(0);
    expect(store.isLazy()).toBe(true);
    const diff = store.getDiff()!;
    expect(diff.files.map((f) => f.path)).toEqual(files.map((f) => f.path).sort());
    expect(diff.files.every((f) => f.hunks.length === 0)).toBe(true);
    expect(store.getFileList()?.files).toHaveLength(files.length);
    expect(store.getFileHunkState("big/new.ts")).toBe("idle");

    store.requestHunks("big/new.ts");
    expect(store.getFileHunkState("big/new.ts")).toBe("loading");

    await vi.waitFor(() => {
      expect(store.getFileHunkState("big/new.ts")).toBe("loaded");
    });
    expect(POST).toHaveBeenCalledTimes(1);
    const [path, init] = POST.mock.calls[0]!;
    expect(path).toBe(HUNKS_PATH);
    expect(init.body).toEqual({
      expect_from: "base1",
      expect_to: "head1",
      paths: ["big/new.ts", "big/old.ts"],
    });
    expect(
      (init as unknown as { params: unknown }).params,
    ).toEqual({ path: { owner: "acme", name: "widget", number: 1 } });

    const loaded = store.getDiff()!.files.find((f) => f.path === "big/new.ts")!;
    expect(loaded.hunks).toHaveLength(1);
    // Replaced in place: order is unchanged.
    expect(store.getDiff()!.files.map((f) => f.path)).toEqual(
      files.map((f) => f.path).sort(),
    );
    expect(store.isWorktreeChanged()).toBe(false);
  });

  it("sends scope and whitespace in the hunks body", async () => {
    const files = largeSummaryFiles();
    installFetch({ files });
    const { client, POST } = makeClient({ hunks: hunksFrom(files) });
    localStorage.setItem("diff-hide-whitespace", "true");
    const store = createDiffStore({ client });
    store.setActivePR("acme", "widget", 1);
    await store.loadCommits();
    await store.selectCommit("c1");

    store.requestHunks("big/a.ts");
    await vi.waitFor(() => {
      expect(store.getFileHunkState("big/a.ts")).toBe("loaded");
    });
    expect(POST.mock.calls[0]![1].body).toEqual({
      commit: "c1",
      whitespace: "hide",
      expect_from: "base1",
      expect_to: "head1",
      paths: ["big/a.ts"],
    });
  });

  it("count mismatch sets isWorktreeChanged", async () => {
    const files = largeSummaryFiles();
    installFetch({ files });
    const changed = files.map((f) =>
      f.path === "big/a.ts" ? { ...f, additions: f.additions + 3 } : f,
    );
    const { client } = makeClient({ hunks: hunksFrom(changed) });
    const store = createDiffStore({ client });
    await store.loadDiff("acme", "widget", 1);

    store.requestHunks("big/a.ts");
    await vi.waitFor(() => {
      expect(store.getFileHunkState("big/a.ts")).toBe("loaded");
    });
    expect(store.isWorktreeChanged()).toBe(true);
  });

  it("a 409 sets isWorktreeChanged and does not reload", async () => {
    const files = largeSummaryFiles();
    const log = installFetch({ files });
    const { client } = makeClient({
      hunks: () => ({
        data: undefined,
        error: { detail: "diff revisions changed" },
        response: { status: 409 },
      }),
    });
    const store = createDiffStore({ client });
    await store.loadDiff("acme", "widget", 1);
    const before = log.urls.length;

    store.requestHunks("big/a.ts");
    await vi.waitFor(() => {
      expect(store.getFileHunkState("big/a.ts")).toBe("error");
    });
    expect(store.isWorktreeChanged()).toBe(true);
    expect(log.urls.length).toBe(before);
  });

  it("a non-409 hunk failure marks error without worktreeChanged", async () => {
    const files = largeSummaryFiles();
    installFetch({ files });
    const { client } = makeClient({
      hunks: () => ({
        data: undefined,
        error: { detail: "boom" },
        response: { status: 500 },
      }),
    });
    const store = createDiffStore({ client });
    await store.loadDiff("acme", "widget", 1);

    store.requestHunks("big/a.ts");
    await vi.waitFor(() => {
      expect(store.getFileHunkState("big/a.ts")).toBe("error");
    });
    expect(store.isWorktreeChanged()).toBe(false);
  });

  it("a scope change drops loaded hunks and resets the worktree flag", async () => {
    const files = largeSummaryFiles();
    installFetch({ files });
    const changed = files.map((f) => ({ ...f, additions: f.additions + 1 }));
    const { client } = makeClient({ hunks: hunksFrom(changed) });
    const store = createDiffStore({ client });
    store.setActivePR("acme", "widget", 1);
    await store.loadDiff("acme", "widget", 1);
    store.requestHunks("big/a.ts");
    await vi.waitFor(() => {
      expect(store.getFileHunkState("big/a.ts")).toBe("loaded");
    });
    expect(store.isWorktreeChanged()).toBe(true);

    await store.loadCommits();
    await store.selectCommit("c1");
    expect(store.getFileHunkState("big/a.ts")).toBe("idle");
    expect(store.isWorktreeChanged()).toBe(false);
    expect(store.getDiff()!.files.every((f) => f.hunks.length === 0)).toBe(true);
  });

  it("patchset scope never goes lazy", async () => {
    const files = largeSummaryFiles();
    const log = installFetch({ files });
    const { client, POST } = makeClient();
    const store = createDiffStore({ client });
    store.setActivePR("acme", "widget", 1);

    store.selectPatchsets(1, 2);
    await vi.waitFor(() => {
      expect(store.getDiff()).not.toBeNull();
    });

    expect(store.isLazy()).toBe(false);
    const diffUrls = log.urls.filter(isDiffUrl);
    expect(diffUrls).toHaveLength(1);
    expect(diffUrls[0]).toContain("from_patchset=1");
    // /files never receives patchset params.
    for (const u of log.urls.filter((u) => u.includes("/files"))) {
      expect(u).not.toContain("patchset");
    }
    expect(POST).not.toHaveBeenCalled();
  });

  it("fetches globs once per repo", async () => {
    installFetch({ files: [file("a.ts")] });
    const { client, GET } = makeClient({ globs: ["gen/**"] });
    const store = createDiffStore({ client });
    await store.loadDiff("acme", "widget", 1);
    await store.loadDiff("acme", "widget", 2);

    const globCalls = GET.mock.calls.filter(([p]) => p === GLOBS_PATH);
    expect(globCalls).toHaveLength(1);
    expect(store.getCollapseGlobs()).toEqual(["gen/**"]);
  });
});

describe("diff store collapse rules", () => {
  async function loadedStore(globs: string[] = []) {
    installFetch({
      files: [
        file("gen/api.pb.go"),
        file("src/main.go"),
        file("old.txt", { status: "deleted", additions: 0, deletions: 3 }),
        file("huge.json", { additions: 1600, deletions: 0 }),
      ],
    });
    const mock = makeClient({ globs });
    const store = createDiffStore({ client: mock.client });
    await store.loadDiff("acme", "widget", 1);
    return { store, ...mock };
  }

  it("resolves default rules with reasons", async () => {
    const { store } = await loadedStore(["gen/**"]);
    const assert = (path: string, collapsed: boolean, reason: string | null) => {
      expect(store.isFileCollapsed("acme", "widget", 1, path)).toBe(collapsed);
      expect(store.getCollapseReason(path)).toBe(reason);
    };
    assert("gen/api.pb.go", true, "pattern");
    assert("old.txt", true, "deleted");
    assert("huge.json", true, "large");
    assert("src/main.go", false, null);
  });

  it("toggle writes explicit overrides in both directions", async () => {
    const { store } = await loadedStore(["gen/**"]);
    store.toggleFileCollapsed("acme", "widget", 1, "gen/api.pb.go");
    expect(store.isFileCollapsed("acme", "widget", 1, "gen/api.pb.go")).toBe(false);
    store.toggleFileCollapsed("acme", "widget", 1, "src/main.go");
    expect(store.isFileCollapsed("acme", "widget", 1, "src/main.go")).toBe(true);
    expect(store.getCollapseReason("src/main.go")).toBe("user");

    const stored = JSON.parse(localStorage.getItem("diff-collapse-overrides")!);
    expect(stored).toEqual({
      "acme/widget#1": { "gen/api.pb.go": false, "src/main.go": true },
    });
  });

  it("a user expand of a glob-matched file persists across store re-creation", async () => {
    const first = await loadedStore(["gen/**"]);
    expect(first.store.isFileCollapsed("acme", "widget", 1, "gen/api.pb.go")).toBe(true);
    first.store.toggleFileCollapsed("acme", "widget", 1, "gen/api.pb.go");

    const second = await loadedStore(["gen/**"]);
    expect(second.store.isFileCollapsed("acme", "widget", 1, "gen/api.pb.go")).toBe(false);
    expect(second.store.getCollapseReason("gen/api.pb.go")).toBeNull();
  });

  it("migrates the old diff-collapsed-files format", async () => {
    localStorage.setItem(
      "diff-collapsed-files",
      JSON.stringify({ "acme/widget#1": ["src/main.go"], "acme/other#2": ["x.go"] }),
    );
    const { store } = await loadedStore();
    expect(store.isFileCollapsed("acme", "widget", 1, "src/main.go")).toBe(true);
    expect(store.getCollapseReason("src/main.go")).toBe("user");
    expect(JSON.parse(localStorage.getItem("diff-collapse-overrides")!)).toEqual({
      "acme/widget#1": { "src/main.go": true },
      "acme/other#2": { "x.go": true },
    });
    expect(localStorage.getItem("diff-collapsed-files")).toBeNull();
  });

  it("setFilesCollapsed records explicit choices for every path", async () => {
    const { store } = await loadedStore(["gen/**"]);
    store.setFilesCollapsed(["gen/api.pb.go", "src/main.go"], false);
    expect(store.isFileCollapsed("acme", "widget", 1, "gen/api.pb.go")).toBe(false);
    store.setFilesCollapsed(["gen/api.pb.go", "src/main.go"], true);
    expect(store.getCollapseReason("gen/api.pb.go")).toBe("user");
    expect(store.getCollapseReason("src/main.go")).toBe("user");
  });

  it("expandForJump expands only collapsed files", async () => {
    const { store } = await loadedStore();
    store.expandForJump("old.txt");
    expect(store.isFileCollapsed("acme", "widget", 1, "old.txt")).toBe(false);
    store.expandForJump("src/main.go");
    const stored = JSON.parse(localStorage.getItem("diff-collapse-overrides")!);
    expect(stored["acme/widget#1"]).toEqual({ "old.txt": false });
  });

  it("saveCollapseGlobs PUTs and applies the new globs", async () => {
    const { store, PUT } = await loadedStore();
    expect(store.isFileCollapsed("acme", "widget", 1, "src/main.go")).toBe(false);
    await store.saveCollapseGlobs(["src/**"]);
    expect(PUT).toHaveBeenCalledWith(GLOBS_PATH, {
      params: { path: { owner: "acme", name: "widget", number: 1 } },
      body: { globs: ["src/**"] },
    });
    expect(store.getCollapseGlobs()).toEqual(["src/**"]);
    expect(store.getCollapseReason("src/main.go")).toBe("pattern");
  });
});
