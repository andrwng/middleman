# Local Review Threads — Phase 1b (Frontend) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Render the persisted local-worktree review threads inline on the diff and rewire the local "Submit review" button to create them — making Phase 1a's backend visible and usable in the UI.

**Architecture:** A new Svelte 5 rune store (`reviewThreads`) wraps the Phase-1a REST endpoints via the generated client (mirroring `worktreeSession`). A `ReviewThreadCard` component (modeled on `AIThreadCard`) renders each thread at its `(path,line,side)` anchor — slotted into `DiffFile` exactly where `AIThreadCard` already renders. `ReviewPanel`'s local submit creates threads from the inline draft comments instead of compiling a `review_feedback` turn.

**Tech Stack:** Svelte 5 (runes), TypeScript, the generated `openapi-fetch` client (`MiddlemanClient`), Vitest + @testing-library/svelte. Frontend tooling is **bun** (never npm).

**Spec:** `docs/superpowers/specs/2026-05-29-local-review-threads-design.md` (Frontend section). **Depends on:** Phase 1a (backend + generated client), already merged on this branch.

---

## Scope & behavior change

This is **Phase 1b, frontend only**. In scope: the `reviewThreads` store, `ReviewThreadCard`, mounting threads on the diff, loading them in the diff view, and the submit-seam change. The card supports **reply / hide / unhide / resolve** — but **not** an "Apply/Go" action or a submit mode picker (those are Phase 2, with the discuss/apply agent).

**Behavior change to call out:** today the local "Submit review" flattens drafts into a `review_feedback` turn that the Claude session acts on immediately. After 1b it instead **persists the drafts as threads** (persist-only). The agent-on-submit returns in **Phase 2** via the mode picker. The Activity tab (`WorktreeConversation` + `worktreeSession`) is untouched — you can still chat with the agent there.

## Patterns to follow (read first)

- Store via generated client: `packages/ui/src/stores/worktreeSession.svelte.ts` — `client.GET/POST("/repos/{owner}/{name}/pulls/{number}/…", { params: { path }, body })`, `{ data, error }` handling, single-active keying.
- Anchored card + store-with-anchor-query: `packages/ui/src/components/diff/AIThreadCard.svelte` and `packages/ui/src/stores/ai.svelte.ts` (`getThreadsAtAnchor`, `start(owner,name,number)`/`stop()` called from `DiffView.svelte:29-35`).
- Inline mount points: `packages/ui/src/components/diff/DiffFile.svelte` — `getAIThreadsAtAnchor` defined at `:385`, rendered at `:892`, `:916`, `:1013`. Store import at `:6`.
- Context wiring: `packages/ui/src/Provider.svelte:256-276` (the `si: StoreInstances` object; `worktreeSession: createWorktreeSessionStore({ client: cl })` at `:275`) and `packages/ui/src/types.ts:119-143` (`StoreInstances`).
- Submit seam + draft shape: `packages/ui/src/components/diff/ReviewPanel.svelte` (`onSubmit`, `isLocal` branch ~`:100-118`) and `DraftComment` in `packages/ui/src/stores/diff.svelte.ts` (`{ id, path, line, side, startLine?, commitSha, body, createdAt, inReplyTo? }`).
- Test patterns: `packages/ui/src/stores/diff.refresh.test.ts` (stub `MiddlemanClient` with `vi.fn()` GET/POST) and `packages/ui/src/components/diff/CommitListItem.test.ts` (`render` from `@testing-library/svelte`).
- Generated types: `import type { components } from "../api/generated/schema.js"`; e.g. `components["schemas"]["ReviewThreadResponse"]`.

## File structure

- Create: `packages/ui/src/stores/reviewThreads.svelte.ts` — the store.
- Create: `packages/ui/src/stores/reviewThreads.svelte.test.ts` — store tests.
- Create: `packages/ui/src/components/diff/ReviewThreadCard.svelte` — the card.
- Create: `packages/ui/src/components/diff/ReviewThreadCard.test.ts` — card render test.
- Modify: `packages/ui/src/types.ts` — add `reviewThreads` to `StoreInstances`.
- Modify: `packages/ui/src/Provider.svelte` — instantiate + register the store.
- Modify: `packages/ui/src/components/diff/DiffFile.svelte` — anchor query + render the card.
- Modify: `packages/ui/src/components/diff/DiffView.svelte` — load/clear the store per worktree.
- Modify: `packages/ui/src/components/diff/ReviewPanel.svelte` — local submit creates threads.

Run frontend commands from `packages/ui/`. Tests: `bunx vitest run <file>`. Typecheck: `bunx svelte-check --tsconfig ./tsconfig.json`. Build: `bun run build`.

---

### Task 1: `reviewThreads` store

**Files:**
- Create: `packages/ui/src/stores/reviewThreads.svelte.ts`
- Test: `packages/ui/src/stores/reviewThreads.svelte.test.ts`

- [ ] **Step 1: Write the failing test**

Create `packages/ui/src/stores/reviewThreads.svelte.test.ts`:

```ts
import { describe, expect, it, vi } from "vitest";
import { createReviewThreadsStore } from "./reviewThreads.svelte.js";
import type { MiddlemanClient } from "../types.js";

function thread(over: Record<string, unknown> = {}) {
  return {
    id: 1, path: "a.go", side: "RIGHT", line: 12, commit_sha: "abc",
    status: "open", hidden: false, created_at: "", updated_at: "",
    comments: [{ id: 1, author: "user", body: "root", created_at: "" }],
    ...over,
  };
}

function stubClient(over: Partial<Record<"GET" | "POST", unknown>> = {}): MiddlemanClient {
  return {
    GET: vi.fn(async () => ({ data: { threads: [thread()] }, error: undefined })),
    POST: vi.fn(async () => ({ data: thread(), error: undefined })),
    ...over,
  } as unknown as MiddlemanClient;
}

describe("reviewThreads store", () => {
  it("loads threads for a local worktree and queries by anchor", async () => {
    const client = stubClient();
    const store = createReviewThreadsStore({ client });
    await store.load("local", "demo", 7);
    expect(client.GET).toHaveBeenCalledWith(
      "/repos/{owner}/{name}/pulls/{number}/review-threads",
      { params: { path: { owner: "local", name: "demo", number: 7 } } },
    );
    expect(store.getThreads()).toHaveLength(1);
    expect(store.getThreadsAtAnchor("a.go", 12, "RIGHT")).toHaveLength(1);
    expect(store.getThreadsAtAnchor("a.go", 99, "RIGHT")).toHaveLength(0);
  });

  it("does not call the API for non-local sources", async () => {
    const client = stubClient();
    const store = createReviewThreadsStore({ client });
    await store.load("acme", "widget", 1);
    expect(client.GET).not.toHaveBeenCalled();
    expect(store.getThreads()).toHaveLength(0);
  });

  it("createThreads maps drafts to the request body and replaces state", async () => {
    const post = vi.fn(async () => ({ data: { threads: [thread(), thread({ id: 2, path: "b.go" })] }, error: undefined }));
    const client = stubClient({ POST: post });
    const store = createReviewThreadsStore({ client });
    await store.load("local", "demo", 7);
    const ok = await store.createThreads([
      { path: "a.go", side: "RIGHT", line: 12, commitSha: "abc", body: "rename" },
      { path: "b.go", side: "RIGHT", line: 3, startLine: 1, commitSha: "abc", body: "extract" },
    ]);
    expect(ok).toBe(true);
    expect(post).toHaveBeenCalledWith(
      "/repos/{owner}/{name}/pulls/{number}/review-threads",
      {
        params: { path: { owner: "local", name: "demo", number: 7 } },
        body: { threads: [
          { path: "a.go", side: "RIGHT", line: 12, commit_sha: "abc", body: "rename" },
          { path: "b.go", side: "RIGHT", line: 3, start_line: 1, commit_sha: "abc", body: "extract" },
        ] },
      },
    );
    expect(store.getThreads()).toHaveLength(2);
  });

  it("addComment/resolve upsert the returned thread", async () => {
    const post = vi.fn(async () => ({ data: thread({ status: "resolved" }), error: undefined }));
    const client = stubClient({ POST: post });
    const store = createReviewThreadsStore({ client });
    await store.load("local", "demo", 7);
    const ok = await store.resolve(1);
    expect(ok).toBe(true);
    expect(post).toHaveBeenCalledWith(
      "/repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}/resolve",
      { params: { path: { owner: "local", name: "demo", number: 7, thread_id: 1 } } },
    );
    expect(store.getThreads()[0]!.status).toBe("resolved");
  });

  it("surfaces API errors", async () => {
    const client = stubClient({ GET: vi.fn(async () => ({ data: undefined, error: { detail: "boom" } })) });
    const store = createReviewThreadsStore({ client });
    await store.load("local", "demo", 7);
    expect(store.getError()).toBe("boom");
  });
});
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd packages/ui && bunx vitest run src/stores/reviewThreads.svelte.test.ts`
Expected: FAIL — cannot find module `./reviewThreads.svelte.js` / `createReviewThreadsStore` is not a function.

- [ ] **Step 3: Implement the store**

Create `packages/ui/src/stores/reviewThreads.svelte.ts`:

```ts
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

  async function createThreads(drafts: ReviewThreadDraftInput[]): Promise<boolean> {
    try {
      const { data, error: err } = await client.POST(
        "/repos/{owner}/{name}/pulls/{number}/review-threads",
        {
          params: { path: { owner, name, number } },
          body: {
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

  async function action(
    verb: "hide" | "unhide" | "resolve", threadID: number,
  ): Promise<boolean> {
    try {
      const { data, error: err } = await client.POST(
        `/repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}/${verb}` as
          "/repos/{owner}/{name}/pulls/{number}/review-threads/{thread_id}/resolve",
        { params: { path: { owner, name, number, thread_id: threadID } } },
      );
      if (err) throw new Error(detail(err, `failed to ${verb} thread`));
      if (data) upsert(data);
      return true;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
      return false;
    }
  }

  const hide = (id: number) => action("hide", id);
  const unhide = (id: number) => action("unhide", id);
  const resolve = (id: number) => action("resolve", id);

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
    load, createThreads, addComment, hide, unhide, resolve, clear,
  };
}

export type ReviewThreadsStore = ReturnType<typeof createReviewThreadsStore>;
```

> Note on `action()`: the four action endpoints (`/hide`, `/unhide`, `/resolve`, and the comment route) all return a `ReviewThreadResponse`, so the templated-path cast keeps openapi-fetch's typing happy while sharing one helper. If `bunx svelte-check` complains about the template-literal path type, replace `hide`/`unhide`/`resolve` with three inline `client.POST("…literal…", …)` calls (literal path strings, no cast) — same body/params.

- [ ] **Step 4: Run to verify it passes**

Run: `cd packages/ui && bunx vitest run src/stores/reviewThreads.svelte.test.ts`
Expected: PASS (5 tests).

- [ ] **Step 5: Commit**

```bash
git add packages/ui/src/stores/reviewThreads.svelte.ts packages/ui/src/stores/reviewThreads.svelte.test.ts
git commit -m "feat(ui): reviewThreads store over the review-thread API"
```

---

### Task 2: Wire the store into context

**Files:**
- Modify: `packages/ui/src/types.ts:119-143`
- Modify: `packages/ui/src/Provider.svelte:275`

No new test — verified by typecheck (the store is consumed in later tasks).

- [ ] **Step 1: Add to `StoreInstances`**

In `packages/ui/src/types.ts`, add the import near the other store-type imports and the field to the interface. Find the line `worktreeSession: WorktreeSessionStore;` (~`:138`) and add directly after it:

```ts
  reviewThreads: ReviewThreadsStore;
```

Add the type import alongside the existing store-type imports (mirror how `WorktreeSessionStore` is imported — `grep -n "WorktreeSessionStore" packages/ui/src/types.ts` to find the import line, then add):

```ts
import type { ReviewThreadsStore } from "./stores/reviewThreads.svelte.js";
```

- [ ] **Step 2: Instantiate in `Provider.svelte`**

In `packages/ui/src/Provider.svelte`, add the import near the other `create*Store` imports (`grep -n "createWorktreeSessionStore" packages/ui/src/Provider.svelte` to find it), then add the field to the `si` object right after `worktreeSession: createWorktreeSessionStore({ client: cl }),` (`:275`):

```ts
      reviewThreads: createReviewThreadsStore({ client: cl }),
```

Import line (next to the other store imports):

```ts
  import { createReviewThreadsStore } from "./stores/reviewThreads.svelte.js";
```

- [ ] **Step 3: Typecheck**

Run: `cd packages/ui && bunx svelte-check --tsconfig ./tsconfig.json 2>&1 | tail -5`
Expected: no new errors referencing `reviewThreads` (pre-existing unrelated warnings, if any, are fine).

- [ ] **Step 4: Commit**

```bash
git add packages/ui/src/types.ts packages/ui/src/Provider.svelte
git commit -m "feat(ui): register reviewThreads store in the app context"
```

---

### Task 3: `ReviewThreadCard` component

**Files:**
- Create: `packages/ui/src/components/diff/ReviewThreadCard.svelte`
- Test: `packages/ui/src/components/diff/ReviewThreadCard.test.ts`

- [ ] **Step 1: Write the failing render test**

Create `packages/ui/src/components/diff/ReviewThreadCard.test.ts`:

```ts
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, fireEvent } from "@testing-library/svelte";

const resolve = vi.fn(async () => true);
const hide = vi.fn(async () => true);
const addComment = vi.fn(async () => true);

vi.mock("../../context.js", () => ({
  getStores: () => ({ reviewThreads: { resolve, hide, unhide: vi.fn(), addComment } }),
}));

import ReviewThreadCard from "./ReviewThreadCard.svelte";

function thread(over: Record<string, unknown> = {}) {
  return {
    id: 5, path: "a.go", side: "RIGHT", line: 12, commit_sha: "abc1234",
    status: "open", hidden: false, created_at: "", updated_at: "",
    comments: [
      { id: 1, author: "user", body: "rename this", created_at: "" },
      { id: 2, author: "agent", body: "agreed", created_at: "" },
    ],
    ...over,
  };
}

afterEach(() => { cleanup(); vi.clearAllMocks(); });

describe("ReviewThreadCard", () => {
  it("renders the comments and a status chip", () => {
    const { getByText } = render(ReviewThreadCard, { props: { thread: thread() } });
    expect(getByText("rename this")).toBeTruthy();
    expect(getByText("agreed")).toBeTruthy();
    expect(getByText(/open/i)).toBeTruthy();
  });

  it("resolve button calls the store", async () => {
    const { getByTitle } = render(ReviewThreadCard, { props: { thread: thread() } });
    await fireEvent.click(getByTitle("Resolve this thread"));
    expect(resolve).toHaveBeenCalledWith(5);
  });

  it("collapses to a stub when hidden, with an unhide affordance", () => {
    const { getByText, queryByText } = render(ReviewThreadCard, {
      props: { thread: thread({ hidden: true }) },
    });
    expect(getByText(/hidden/i)).toBeTruthy();
    expect(queryByText("rename this")).toBeNull(); // body not shown while hidden
  });
});
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd packages/ui && bunx vitest run src/components/diff/ReviewThreadCard.test.ts`
Expected: FAIL — cannot find `./ReviewThreadCard.svelte`.

- [ ] **Step 3: Implement the component**

Create `packages/ui/src/components/diff/ReviewThreadCard.svelte`. The `<style>` block should be adapted from `AIThreadCard.svelte`'s styles by copying that file's `<style>` and renaming the `.ai-thread*` class selectors to `.review-thread*` (same design tokens / layout; the visual language matches the sibling AI thread cards).

```svelte
<script lang="ts">
  import { getStores } from "../../context.js";
  import { renderMarkdown } from "../../utils/markdown.js";
  import type { ReviewThread } from "../../stores/reviewThreads.svelte.js";

  interface Props {
    thread: ReviewThread;
  }
  const { thread }: Props = $props();

  const { reviewThreads } = getStores();

  const comments = $derived(thread.comments ?? []);
  let reply = $state("");
  let sending = $state(false);

  async function sendReply(): Promise<void> {
    const text = reply.trim();
    if (!text || sending) return;
    sending = true;
    try {
      const ok = await reviewThreads.addComment(thread.id, text);
      if (ok) reply = "";
    } finally {
      sending = false;
    }
  }

  function onReplyKeydown(e: KeyboardEvent): void {
    if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
      e.preventDefault();
      void sendReply();
    }
  }
</script>

{#if thread.hidden}
  <div class="review-thread review-thread--hidden">
    <span class="review-thread__hidden-label">Hidden thread</span>
    <button
      type="button"
      class="review-thread__unhide"
      onclick={() => void reviewThreads.unhide(thread.id)}
    >Show</button>
  </div>
{:else}
  <div class="review-thread">
    <div class="review-thread__header">
      <span class="review-thread__badge">Review</span>
      <span class="review-thread__anchor">
        {thread.side === "LEFT" ? "−" : "+"}{thread.start_line != null &&
        thread.start_line !== thread.line
          ? `${thread.start_line}–${thread.line}`
          : thread.line}
      </span>
      <span class="review-thread__status">{thread.status}</span>
      <span class="review-thread__commit" title="Anchored to this commit">
        {thread.commit_sha.slice(0, 7)}
      </span>
      <button
        type="button"
        class="review-thread__action"
        title="Resolve this thread"
        onclick={() => void reviewThreads.resolve(thread.id)}
      >Resolve</button>
      <button
        type="button"
        class="review-thread__action"
        title="Hide this thread"
        onclick={() => void reviewThreads.hide(thread.id)}
      >Hide</button>
    </div>

    {#each comments as c (c.id)}
      <div class="review-thread__comment">
        <span class="review-thread__author review-thread__author--{c.author}">
          {c.author === "agent" ? "Claude" : "You"}
        </span>
        <div class="review-thread__body markdown-body">
          {@html renderMarkdown(c.body, {})}
        </div>
      </div>
    {/each}

    {#if thread.status !== "resolved"}
      <div class="review-thread__reply">
        <textarea
          bind:value={reply}
          class="review-thread__reply-input"
          placeholder="Reply... (⌘/Ctrl+Enter to send)"
          rows="2"
          onkeydown={onReplyKeydown}
        ></textarea>
        <button
          type="button"
          class="review-thread__send"
          disabled={sending || !reply.trim()}
          onclick={() => void sendReply()}
        >Send</button>
      </div>
    {/if}
  </div>
{/if}

<style>
  /* Adapt from AIThreadCard.svelte: copy its <style> block and rename
     .ai-thread* selectors to .review-thread*. Add the two below. */
  .review-thread--hidden {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 12px;
    color: var(--text-muted);
  }
  .review-thread__author {
    font-size: 10px;
    font-weight: 700;
    text-transform: uppercase;
    color: var(--text-muted);
  }
</style>
```

> `renderMarkdown(c.body, {})` — the second arg is the repo-context for deep-linking; an empty object is fine here (Phase 2 can pass real context). Confirm `renderMarkdown`'s signature accepts it (`AIThreadCard.svelte:178` passes a `repoCtx` object); if the empty object trips the types, pass `undefined` or the minimal `{ owner, name }` if the component is given them.

- [ ] **Step 4: Run to verify it passes**

Run: `cd packages/ui && bunx vitest run src/components/diff/ReviewThreadCard.test.ts`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add packages/ui/src/components/diff/ReviewThreadCard.svelte packages/ui/src/components/diff/ReviewThreadCard.test.ts
git commit -m "feat(ui): ReviewThreadCard renders a review thread with reply/hide/resolve"
```

---

### Task 4: Mount review threads on the diff

**Files:**
- Modify: `packages/ui/src/components/diff/DiffFile.svelte` (`:6`, `:385`, and the three render sites `:892`, `:916`, `:1013`)

No new test — `DiffFile` is large and integration-heavy; verified by typecheck + the final visual check. The logic it adds is a thin mirror of the AI-thread rendering.

- [ ] **Step 1: Add the store + anchor query**

In `packages/ui/src/components/diff/DiffFile.svelte`, extend the `getStores()` destructure at `:6`:

```ts
  const { diff: diffStore, ai: aiStore, detail: detailStore, reviewThreads: reviewThreadsStore } = getStores();
```

Add the import for the card near the other card imports (after `import AIThreadCard from "./AIThreadCard.svelte";` at `:14`):

```ts
  import ReviewThreadCard from "./ReviewThreadCard.svelte";
```

Add an anchor-query helper next to `getAIThreadsAtAnchor` (`:385`):

```ts
  function getReviewThreadsAtAnchor(line: number, side: "LEFT" | "RIGHT") {
    return reviewThreadsStore.getThreadsAtAnchor(file.path, line, side);
  }
```

- [ ] **Step 2: Render the card at all three AI-thread sites**

At each of the three places `getAIThreadsAtAnchor(...)` is rendered (`:892` left, `:916` right, `:1013` unified), add an adjacent block. For the `:892`/`:916` sites the anchor var is `leftAnchor`/`rightAnchor`; for `:1013` it is `anchor`. Mirror the existing AI block. Example for the left site (`:891-895`):

```svelte
                  {#if leftAnchor}
                    {#each getAIThreadsAtAnchor(leftAnchor.line, leftAnchor.side) as thread (thread.id)}
                      <AIThreadCard {thread} repoOwner={owner} repoName={name} />
                    {/each}
                    {#each getReviewThreadsAtAnchor(leftAnchor.line, leftAnchor.side) as rt (rt.id)}
                      <ReviewThreadCard thread={rt} />
                    {/each}
                  {/if}
```

Do the equivalent at the right site (`rightAnchor`) and the unified site (`anchor`):

```svelte
                    {#each getReviewThreadsAtAnchor(anchor.line, anchor.side) as rt (rt.id)}
                      <ReviewThreadCard thread={rt} />
                    {/each}
```

- [ ] **Step 3: Typecheck**

Run: `cd packages/ui && bunx svelte-check --tsconfig ./tsconfig.json 2>&1 | tail -8`
Expected: no new errors in `DiffFile.svelte`.

- [ ] **Step 4: Commit**

```bash
git add packages/ui/src/components/diff/DiffFile.svelte
git commit -m "feat(ui): render review threads inline on the diff"
```

---

### Task 5: Load review threads in the diff view

**Files:**
- Modify: `packages/ui/src/components/diff/DiffView.svelte:29-35`

- [ ] **Step 1: Load on mount, clear on teardown**

In `packages/ui/src/components/diff/DiffView.svelte`, the `onMount` already does `aiStore.start(owner, name, number)` / `briefStore.start(...)` and the cleanup does `aiStore.stop()`. Add the review-threads store. First extend the `getStores()` destructure in that file to include `reviewThreads: reviewThreadsStore` (find the destructure with `grep -n "getStores()" packages/ui/src/components/diff/DiffView.svelte`). Then in the mount effect, after the `aiStore.start(...)` line:

```ts
    void reviewThreadsStore.load(owner, name, number);
```

And in the cleanup (next to `aiStore.stop()`):

```ts
    reviewThreadsStore.clear();
```

(The store's `load` no-ops the API for non-local owners, so this is safe for remote PRs — it just clears.)

- [ ] **Step 2: Typecheck**

Run: `cd packages/ui && bunx svelte-check --tsconfig ./tsconfig.json 2>&1 | tail -5`
Expected: no new errors.

- [ ] **Step 3: Commit**

```bash
git add packages/ui/src/components/diff/DiffView.svelte
git commit -m "feat(ui): load review threads when opening a local worktree diff"
```

---

### Task 6: Local submit creates threads

**Files:**
- Modify: `packages/ui/src/components/diff/ReviewPanel.svelte` (`onSubmit`, `isLocal` branch ~`:100-118`)

- [ ] **Step 1: Replace the local submit branch**

In `packages/ui/src/components/diff/ReviewPanel.svelte`, add `reviewThreads: reviewThreadsStore` to the `getStores()` destructure (find it with `grep -n "getStores()" packages/ui/src/components/diff/ReviewPanel.svelte`). Replace the `isLocal` branch body (currently compiles `review_feedback` and calls `worktreeSession.submitTurn`) with thread creation from the inline draft comments:

```ts
    if (isLocal) {
      // Local worktrees persist drafts as review threads (Phase 1b).
      // Only inline comments become threads; the review summary/event
      // are not used here (the discuss/apply agent + mode picker land
      // in Phase 2). Reply-drafts (inReplyTo) are not part of the local
      // flow — replies are added directly on a thread card.
      const drafts = draft.comments
        .filter((c) => c.inReplyTo == null)
        .map((c) => ({
          path: c.path,
          side: c.side,
          line: c.line,
          ...(c.startLine != null ? { startLine: c.startLine } : {}),
          commitSha: c.commitSha,
          body: c.body,
        }));
      if (drafts.length === 0) {
        errorMsg = "Add at least one inline comment to create review threads";
        submitting = false; // outer onSubmit already set submitting = true
        return;
      }
      try {
        const ok = await reviewThreadsStore.createThreads(drafts);
        if (!ok) {
          errorMsg = reviewThreadsStore.getError() ?? "Failed to create review threads";
          return;
        }
        diffStore.clearDraft();
        onclose();
      } finally {
        submitting = false;
      }
      return;
    }
```

Also remove the now-unused `compileReviewFeedback` function and the `worktreeSession` destructure **only if** nothing else in the file uses them (`grep -n "compileReviewFeedback\|worktreeSession" packages/ui/src/components/diff/ReviewPanel.svelte`). If `worktreeSession` is used elsewhere in the file, leave it.

- [ ] **Step 2: Typecheck**

Run: `cd packages/ui && bunx svelte-check --tsconfig ./tsconfig.json 2>&1 | tail -8`
Expected: no new errors. (If `compileReviewFeedback` is left unused it will warn — remove it.)

- [ ] **Step 3: Commit**

```bash
git add packages/ui/src/components/diff/ReviewPanel.svelte
git commit -m "feat(ui): local Submit persists drafts as review threads"
```

---

### Task 7: Full verification

- [ ] **Step 1: Run the whole frontend test suite**

Run: `cd packages/ui && bunx vitest run 2>&1 | tail -15`
Expected: PASS, including the new `reviewThreads.svelte.test.ts` and `ReviewThreadCard.test.ts`. No regressions.

- [ ] **Step 2: Typecheck the whole app**

Run: `cd packages/ui && bunx svelte-check --tsconfig ./tsconfig.json 2>&1 | tail -10`
Expected: no new errors attributable to this work.

- [ ] **Step 3: Build the frontend**

Run: `cd packages/ui && bun run build 2>&1 | tail -10`
Expected: build succeeds (this is what gets embedded into the Go binary).

- [ ] **Step 4: Commit any build-output changes if the repo tracks them**

`git status --short` — if `internal/web/dist/` or similar generated output is tracked and changed, stage and commit it; otherwise nothing to do.

```bash
git add -- internal/web/dist 2>/dev/null || true
git diff --cached --quiet || git commit -m "chore(ui): rebuild embedded frontend for review threads"
```

> Optional visual check (not required for this kept-unmerged branch; do if iterating on the look): run the app (`make dev` + `make frontend-dev`), open a local worktree's Review tab, draft an inline comment, Submit, and confirm the thread renders at its line with reply/hide/resolve. The `capture-playwright` skill can screenshot it.

---

## Self-review

**Spec coverage (Phase 1b frontend slice):**
- Persisted threads render inline on the worktree diff, anchored by (path,line,side) → Tasks 3, 4. ✓
- Hideable threads (hide → collapsed stub → unhide) → Tasks 1, 3. ✓
- Resolve + in-thread reply (user) → Tasks 1, 3. ✓
- Local "Submit" creates threads from drafts (persist-only; agent + mode picker deferred to Phase 2, called out in Scope) → Task 6. ✓
- Threads loaded when opening a local worktree diff; cleared otherwise → Task 5. ✓
- **Deferred (Phase 2, intentionally absent):** Apply/Go, submit mode picker, discuss/apply agent turns, agent replies in-thread, polling. **Phase 3:** MCP. No frontend for those here.

**Placeholder scan:** none — store, card, and all edits have concrete code. The one pattern-reference (card `<style>` ← `AIThreadCard.svelte`) points at a specific real file's style block to copy+rename, which is concrete and reviewable.

**Type consistency:** `ReviewThread`/`ReviewThreadComment`/`ReviewThreadDraftInput` and the store method names (`load`, `getThreadsAtAnchor`, `createThreads`, `addComment`, `hide`, `unhide`, `resolve`, `clear`) are used consistently across the store (Task 1), context types (Task 2), card (Task 3), DiffFile (Task 4), DiffView (Task 5), and ReviewPanel (Task 6). Request body field names (`commit_sha`, `start_line`, `thread_id`) match the generated client from Phase 1a.

**Known risks to verify during execution:**
- The shared `action()` helper uses a template-literal path with a cast; if `svelte-check` rejects it, split into three literal-path calls (noted inline in Task 1).
- `renderMarkdown`'s second arg: pass `{}` or adjust per its real signature (noted in Task 3).
- Whether `internal/web/dist/` is tracked (Task 7 Step 4 handles both cases).
