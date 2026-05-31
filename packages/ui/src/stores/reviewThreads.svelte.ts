import type { MiddlemanClient } from "../types.js";
import type { components } from "../api/generated/schema.js";

export type ReviewThread = components["schemas"]["ReviewThreadResponse"];
export type ReviewThreadComment = components["schemas"]["ReviewThreadCommentResponse"];

// One inline draft comment to turn into a thread on submit.
export interface ReviewThreadDraftInput {
  path: string;
  side: "LEFT" | "RIGHT";
  line: number;
  startLine?: number;
  commitSha: string;
  body: string;
}

export interface ReviewThreadsStoreOptions {
  client: MiddlemanClient;
}

// Threads for a local worktree review, keyed to the single active
// (owner,name,number). Review threads exist only for local sources, so
// non-local loads clear state and skip the API. Mutations re-read the
// affected thread from the response and upsert it — no polling, because
// Phase 1b has no agent producing async replies.
export function createReviewThreadsStore(opts: ReviewThreadsStoreOptions) {
  const client = opts.client;
  let owner = $state("");
  let name = $state("");
  let number = $state(0);
  let threads = $state<ReviewThread[]>([]);
  let loading = $state(false);
  let error = $state<string | null>(null);

  function getThreads(): ReviewThread[] {
    return threads;
  }
  function isLoading(): boolean {
    return loading;
  }
  function getError(): string | null {
    return error;
  }

  function getThreadsAtAnchor(
    path: string, line: number, side: "LEFT" | "RIGHT",
  ): ReviewThread[] {
    return threads.filter(
      (t) => t.path === path && t.line === line && t.side === side,
    );
  }

  function detail(err: unknown, fallback: string): string {
    return (err as { detail?: string }).detail ?? fallback;
  }

  function upsert(t: ReviewThread): void {
    const i = threads.findIndex((x) => x.id === t.id);
    if (i === -1) {
      threads = [...threads, t];
    } else {
      const next = [...threads];
      next[i] = t;
      threads = next;
    }
  }

  async function load(o: string, n: string, num: number): Promise<void> {
    owner = o;
    name = n;
    number = num;
    if (o !== "local") {
      threads = [];
      return;
    }
    loading = true;
    error = null;
    try {
      const { data, error: err } = await client.GET(
        "/repos/{owner}/{name}/pulls/{number}/review-threads",
        { params: { path: { owner: o, name: n, number: num } } },
      );
      if (err) throw new Error(detail(err, "failed to load review threads"));
      threads = data?.threads ?? [];
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  }

  async function createThreads(
    drafts: ReviewThreadDraftInput[], mode?: string,
  ): Promise<boolean> {
    error = null;
    try {
      const { data, error: err } = await client.POST(
        "/repos/{owner}/{name}/pulls/{number}/review-threads",
        {
          params: { path: { owner, name, number } },
          body: {
            ...(mode ? { mode } : {}),
            threads: drafts.map((d) => ({
              path: d.path,
              side: d.side,
              line: d.line,
              ...(d.startLine != null ? { start_line: d.startLine } : {}),
              commit_sha: d.commitSha,
              body: d.body,
            })),
          },
        },
      );
      if (err) throw new Error(detail(err, "failed to create review threads"));
      threads = data?.threads ?? threads;
      return true;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
      return false;
    }
  }

  async function addComment(
    threadID: number, body: string, author?: "user" | "agent",
  ): Promise<boolean> {
    error = null;
    try {
      const { data, error: err } = await client.POST(
        "/repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}/comments",
        {
          params: { path: { owner, name, number, thread_id: threadID } },
          body: { body, ...(author ? { author } : {}) },
        },
      );
      if (err) throw new Error(detail(err, "failed to add comment"));
      if (data) upsert(data);
      return true;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
      return false;
    }
  }

  async function resolve(threadID: number): Promise<boolean> {
    error = null;
    try {
      const { data, error: err } = await client.POST(
        "/repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}/resolve",
        { params: { path: { owner, name, number, thread_id: threadID } } },
      );
      if (err) throw new Error(detail(err, "failed to resolve thread"));
      if (data) upsert(data);
      return true;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
      return false;
    }
  }

  async function apply(threadID: number): Promise<boolean> {
    error = null;
    try {
      const { data, error: err } = await client.POST(
        "/repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}/apply",
        { params: { path: { owner, name, number, thread_id: threadID } } },
      );
      if (err) throw new Error(detail(err, "failed to apply thread"));
      threads = data?.threads ?? threads;
      return true;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
      return false;
    }
  }

  async function applyAll(): Promise<boolean> {
    error = null;
    try {
      const { data, error: err } = await client.POST(
        "/repos/{owner}/{name}/pulls/{number}/review-threads/apply-all",
        { params: { path: { owner, name, number } } },
      );
      if (err) throw new Error(detail(err, "failed to apply all threads"));
      threads = data?.threads ?? threads;
      return true;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
      return false;
    }
  }

  async function deleteThread(threadID: number): Promise<boolean> {
    error = null;
    try {
      const { data, error: err } = await client.DELETE(
        "/repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}",
        { params: { path: { owner, name, number, thread_id: threadID } } },
      );
      if (err) throw new Error(detail(err, "failed to delete thread"));
      threads = data?.threads ?? threads.filter((t) => t.id !== threadID);
      return true;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
      return false;
    }
  }

  // refresh re-reads the current review's threads without toggling the
  // loading flag — used by the live poll while an agent turn runs and by
  // the SSE data_changed catch-all. No-op when not on a loaded local review.
  async function refresh(): Promise<void> {
    if (owner !== "local" || number === 0) return;
    try {
      const { data, error: err } = await client.GET(
        "/repos/{owner}/{name}/pulls/{number}/review-threads",
        { params: { path: { owner, name, number } } },
      );
      if (err) return; // best-effort; keep current state on transient errors
      threads = data?.threads ?? threads;
    } catch {
      // swallow — refresh is best-effort
    }
  }

  async function hide(threadID: number): Promise<boolean> {
    error = null;
    try {
      const { data, error: err } = await client.POST(
        "/repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}/hide",
        { params: { path: { owner, name, number, thread_id: threadID } } },
      );
      if (err) throw new Error(detail(err, "failed to hide thread"));
      if (data) upsert(data);
      return true;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
      return false;
    }
  }

  async function unhide(threadID: number): Promise<boolean> {
    error = null;
    try {
      const { data, error: err } = await client.POST(
        "/repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}/unhide",
        { params: { path: { owner, name, number, thread_id: threadID } } },
      );
      if (err) throw new Error(detail(err, "failed to unhide thread"));
      if (data) upsert(data);
      return true;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
      return false;
    }
  }

  function clear(): void {
    owner = "";
    name = "";
    number = 0;
    threads = [];
    loading = false;
    error = null;
  }

  return {
    getThreads, getThreadsAtAnchor, isLoading, getError,
    load, createThreads, addComment, hide, unhide, resolve,
    apply, applyAll, deleteThread, refresh, clear,
  };
}

export type ReviewThreadsStore = ReturnType<typeof createReviewThreadsStore>;
