import type { MiddlemanClient } from "../types.js";
import type { components } from "../api/generated/schema.js";

export type ReviewThread = components["schemas"]["ReviewThreadResponse"];
export type ReviewThreadComment = components["schemas"]["ReviewThreadCommentResponse"];

// The agent mode chosen at submit time; persist-only is the no-agent default.
export type ReviewThreadMode = "discuss-first" | "act-immediately" | "persist-only";

// One inline draft comment to turn into a thread on submit.
export interface ReviewThreadDraftInput {
  path: string;
  side: "LEFT" | "RIGHT";
  line: number;
  startLine?: number;
  commitSha: string;
  body: string;
  // Extra authored comments appended after the root, in order (e.g. a
  // promoted Ask-Claude session's Q&A turns).
  comments?: { author: "user" | "agent"; body: string }[];
}

export interface ReviewThreadsStoreOptions {
  client: MiddlemanClient;
}

// Threads for a local worktree review, keyed to the single active
// (owner,name,number). Review threads exist only for local sources, so
// non-local loads clear state and skip the API. Mutations re-read from
// the response: hide/unhide/resolve/comment upsert the single returned
// thread, while apply/applyAll/delete replace the whole list. refresh()
// re-reads silently and is polled by the Provider while an agent turn
// runs, so the agent's async replies and discussed/applied status land
// live.
export function createReviewThreadsStore(opts: ReviewThreadsStoreOptions) {
  const client = opts.client;
  let owner = $state("");
  let name = $state("");
  let number = $state(0);
  let threads = $state<ReviewThread[]>([]);
  let loading = $state(false);
  let error = $state<string | null>(null);
  // The revision `load()` last resolved anchors against. `refresh()` has
  // no caller-supplied `at` of its own -- it re-reads silently during a
  // poll -- so it reuses this rather than falling back to no resolution,
  // which would otherwise snap every moved card back to its recorded line
  // mid-review.
  let lastAt: string | undefined;

  function getThreads(): ReviewThread[] {
    return threads;
  }
  function isLoading(): boolean {
    return loading;
  }
  function getError(): string | null {
    return error;
  }

  // Where a thread should appear in the diff. A resolution, when the server
  // supplied one, wins over the recorded anchor -- that is the whole point
  // of it. "removed" and "unmappable" carry no position of their own, so
  // this answers with the recorded anchor for them; whether they may be
  // shown there at all is isPlaceable's question, not this one.
  function placementFor(t: ReviewThread): { path: string; line: number } {
    const r = t.resolved;
    if (r && (r.state === "current" || r.state === "moved")) {
      return { path: r.path ?? t.path, line: r.line ?? t.line };
    }
    return { path: t.path, line: t.line };
  }

  // Whether this thread may be rendered as an inline card in the diff.
  // Per the spec's resolution table, "removed" and "unmappable" are
  // list-only: the server has positively established that the recorded
  // line is gone, or that it cannot say where it went. Drawing a card at
  // the recorded line anyway would sit the conversation on whatever
  // unrelated code now occupies that number -- confidently and with no
  // label -- which is exactly the misplacement this work exists to
  // remove. Those threads stay in the threads list, badged with their
  // state and openable in place, so nothing is hidden.
  //
  // No resolution at all ("at" not sent, or a thread the server declined
  // to resolve) means no opinion, so the recorded anchor stands.
  function isPlaceable(t: ReviewThread): boolean {
    const state = t.resolved?.state;
    return state === undefined || state === "current" || state === "moved";
  }

  function getThreadsAtAnchor(
    path: string, line: number, side: "LEFT" | "RIGHT",
  ): ReviewThread[] {
    return threads.filter((t) => {
      if (t.side !== side) return false;
      if (!isPlaceable(t)) return false;
      const at = placementFor(t);
      return at.path === path && at.line === line;
    });
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

  async function load(o: string, n: string, num: number, at?: string): Promise<void> {
    owner = o;
    name = n;
    number = num;
    // Only overwrite when this caller actually supplied a revision. A
    // surface with no opinion about resolution (e.g. the doc-review view)
    // must not wipe out the revision refresh() should keep re-resolving
    // against for whoever else is watching the same shared store. The
    // local check matters for the same reason: a non-local load has
    // nothing to resolve and must not leave a revision behind that a
    // later local refresh() would send.
    if (o === "local" && at !== undefined) lastAt = at;
    if (o !== "local") {
      threads = [];
      return;
    }
    loading = true;
    error = null;
    try {
      const { data, error: err } = await client.GET(
        "/repos/{owner}/{name}/pulls/{number}/review-threads",
        { params: { path: { owner: o, name: n, number: num }, query: at ? { at } : {} } },
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
    drafts: ReviewThreadDraftInput[], mode?: ReviewThreadMode,
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
              ...(d.comments ? { comments: d.comments } : {}),
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

  async function editComment(
    threadID: number, commentID: number, body: string,
  ): Promise<boolean> {
    error = null;
    try {
      const { data, error: err } = await client.POST(
        "/repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}/comments/{comment_id}/edit",
        {
          params: { path: { owner, name, number, thread_id: threadID, comment_id: commentID } },
          body: { body },
        },
      );
      if (err) throw new Error(detail(err, "failed to edit comment"));
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

  async function unresolve(threadID: number): Promise<boolean> {
    error = null;
    try {
      const { data, error: err } = await client.POST(
        "/repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}/unresolve",
        { params: { path: { owner, name, number, thread_id: threadID } } },
      );
      if (err) throw new Error(detail(err, "failed to unresolve thread"));
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

  // discuss kicks a read-only discuss turn on a single thread (no message
  // — the agent responds to the thread's existing content). Replaces the
  // list like apply, since the response carries the MR's full thread set.
  async function discuss(threadID: number): Promise<boolean> {
    error = null;
    try {
      const { data, error: err } = await client.POST(
        "/repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}/discuss",
        { params: { path: { owner, name, number, thread_id: threadID } } },
      );
      if (err) throw new Error(detail(err, "failed to start discussion"));
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

  async function ask(threadID: number, body: string): Promise<boolean> {
    error = null;
    try {
      const { data, error: err } = await client.POST(
        "/repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}/ask",
        { params: { path: { owner, name, number, thread_id: threadID } }, body: { body } },
      );
      if (err) throw new Error(detail(err, "failed to ask the agent"));
      if (data) upsert(data);
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
  // A refresh in flight when a mutation resolves can briefly overwrite the
  // optimistic result; the next poll re-syncs, which is fine for a local
  // single-user tool.
  async function refresh(): Promise<void> {
    if (owner !== "local" || number === 0) return;
    try {
      const { data, error: err } = await client.GET(
        "/repos/{owner}/{name}/pulls/{number}/review-threads",
        { params: { path: { owner, name, number }, query: lastAt ? { at: lastAt } : {} } },
      );
      if (err) return; // best-effort; keep current state on transient errors
      threads = data?.threads ?? threads;
    } catch {
      // swallow — refresh is best-effort
    }
  }

  // resolveAt re-reads the loaded review's threads resolved against a new
  // target revision, and remembers it so later refreshes keep using it.
  //
  // Deliberately narrower than load(): the review itself has not changed,
  // only the revision the reader is looking at, so this must not re-key
  // the store or flip the loading flag -- a scope change would otherwise
  // blank the threads list on every commit click. Reusing refresh() also
  // inherits its best-effort error handling: a failed re-resolve leaves
  // the current threads (and their current resolutions) in place rather
  // than emptying the list.
  //
  // A caller with no revision to offer must not clear the one already in
  // effect, so "" is a no-op rather than a reset.
  async function resolveAt(at: string): Promise<void> {
    if (owner !== "local" || number === 0 || at === "") return;
    lastAt = at;
    await refresh();
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
    // A stale revision must not follow the reader into the next PR/worktree
    // they open -- reset it here rather than leaving it for the next
    // load() to overwrite, since a load with no 'at' (see above) won't.
    lastAt = undefined;
  }

  return {
    getThreads, getThreadsAtAnchor, placementFor, isPlaceable, isLoading, getError,
    load, resolveAt, createThreads, addComment, editComment, hide, unhide, resolve, unresolve,
    apply, discuss, applyAll, ask, deleteThread, refresh, clear,
  };
}

export type ReviewThreadsStore = ReturnType<typeof createReviewThreadsStore>;
