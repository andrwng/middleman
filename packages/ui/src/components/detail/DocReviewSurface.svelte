<script lang="ts">
  import { onMount } from "svelte";
  import RenderedMarkdownView from "../diff/RenderedMarkdownView.svelte";
  import ReviewPanel from "../diff/ReviewPanel.svelte";
  import { WORKING_TREE_SENTINEL } from "../../utils/worktreeSentinel.js";
  import { getNavigate, getStores } from "../../context.js";

  interface Props {
    owner: string;
    name: string;
    number: number;
    path: string;
    basePath: string;
  }
  const { owner, name, number, path, basePath }: Props = $props();

  const navigate = getNavigate();
  const { ai: aiStore, diff: diffStore, reviewThreads: reviewThreadsStore } = getStores();

  let reviewPanelOpen = $state(false);

  // Source lines (1-based, RIGHT/new side) that differ from HEAD — i.e. not
  // yet committed — for the currently open doc. Populated by the diff-fetch
  // effect below and passed to RenderedMarkdownView so it can highlight the
  // matching per-line anchor spans.
  let uncommittedLines = $state<Set<number>>(new Set());

  // Per-doc pending draft comment count, shown on the Review button so
  // a reviewer can tell at a glance whether this doc has anything to
  // submit without leaving the doc pane.
  const pendingCount = $derived(diffStore.getDraftCommentsForPath(path).length);

  // In-app navigation uses an unprefixed path; navigate() applies the base
  // prefix internally.
  const reviewRoute = $derived(`/pulls/${owner}/${name}/${number}/files`);

  // The new-tab href is raw browser navigation — the base prefix must be
  // included explicitly because the browser does not apply it.
  const newTabHref = $derived(
    basePath.replace(/\/$/, "") +
      "/pulls/" +
      owner +
      "/" +
      name +
      "/" +
      number +
      "/doc?path=" +
      encodeURIComponent(path),
  );

  // Build the base-prefixed doc-route href for a target worktree path — used
  // as the rewritten cross-doc link's href so a modified/middle click opens it
  // in a new tab natively.
  function docHrefFor(targetPath: string): string {
    return (
      basePath.replace(/\/$/, "") +
      `/pulls/${owner}/${name}/${number}/doc?path=` +
      encodeURIComponent(targetPath)
    );
  }

  // Open a linked doc in docs mode (client-side; unprefixed — navigate applies
  // the base prefix). A #fragment rides along in the URL hash so the target
  // doc scrolls to that section once it renders. Goes through history, so
  // back/forward work.
  function openDoc(targetPath: string, fragment?: string): void {
    navigate(
      `/pulls/${owner}/${name}/${number}/doc?path=${encodeURIComponent(targetPath)}` +
        (fragment ? `#${fragment}` : ""),
    );
  }

  // When this component replaces ReviewSurface (which hosts DiffView),
  // the aiStore.start() and reviewThreadsStore.load() calls that DiffView
  // normally makes on mount won't happen. Trigger them here so that
  // RenderedMarkdownView can display existing AI threads and review threads.
  onMount(() => {
    diffStore.setActivePR(owner, name, number);
    aiStore.start(owner, name, number);
    void reviewThreadsStore.load(owner, name, number);
    return () => {
      aiStore.stop();
      reviewThreadsStore.clear();
      diffStore.setActivePR("", "", 0);
    };
  });

  // Fetch the working-tree-vs-HEAD diff and collect the open doc's added/
  // modified line numbers (type=="add" -> new_num). Re-runs when the open
  // doc changes. Fails soft: any error or non-ok response just leaves
  // uncommittedLines empty rather than blocking the doc from rendering.
  $effect(() => {
    const p = path;
    const [o, n, num] = [owner, name, number];
    let cancelled = false;
    void (async () => {
      try {
        const url =
          `/api/v1/repos/${encodeURIComponent(o)}/${encodeURIComponent(n)}` +
          `/pulls/${num}/diff?commit=${encodeURIComponent(WORKING_TREE_SENTINEL)}`;
        const res = await fetch(url);
        if (!res.ok) throw new Error(`diff ${res.status}`);
        const data = (await res.json()) as {
          files?: Array<{
            path: string;
            hunks?: Array<{ lines?: Array<{ type: string; new_num?: number }> }>;
          }>;
        };
        const file = data.files?.find((f) => f.path === p);
        const s = new Set<number>();
        for (const h of file?.hunks ?? [])
          for (const ln of h.lines ?? [])
            if (ln.type === "add" && ln.new_num != null) s.add(ln.new_num);
        if (!cancelled) uncommittedLines = s;
      } catch {
        if (!cancelled) uncommittedLines = new Set();
      }
    })();
    return () => {
      cancelled = true;
    };
  });
</script>

<div class="doc-surface">
  <div class="doc-header">
    <button class="doc-back" onclick={() => navigate(reviewRoute)}>
      ← Review
    </button>
    <span class="doc-path">{path}</span>
    <button
      class="doc-review"
      disabled={pendingCount === 0}
      title={pendingCount === 0 ? "No pending comments on this doc" : "Finish review for this doc"}
      onclick={() => (reviewPanelOpen = true)}
    >
      Review ({pendingCount})
    </button>
    <a
      class="doc-newtab"
      href={newTabHref}
      target="_blank"
      rel="noopener"
      title="Open in new tab"
    >
      ↗
    </a>
  </div>
  <div class="doc-body">
    <RenderedMarkdownView
      {owner}
      {name}
      {number}
      {path}
      sha={WORKING_TREE_SENTINEL}
      hunks={[]}
      {uncommittedLines}
      commentLayout="gutter"
      docHref={docHrefFor}
      {openDoc}
    />
  </div>
</div>

{#if reviewPanelOpen}
  <ReviewPanel {owner} {name} {number} scopePath={path} onclose={() => (reviewPanelOpen = false)} />
{/if}

<style>
  .doc-surface {
    display: flex;
    flex-direction: column;
    flex: 1;
    min-height: 0;
  }

  .doc-header {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 6px 10px;
    border-bottom: 1px solid var(--border-default);
    flex-shrink: 0;
  }

  .doc-path {
    flex: 1;
    font-size: 12px;
    color: var(--text-secondary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .doc-back {
    font-size: 12px;
    color: var(--text-secondary);
    padding: 2px 6px;
    border-radius: var(--radius-sm);
    transition: color 0.1s, background 0.1s;
  }

  .doc-back:hover {
    color: var(--text-primary);
    background: var(--bg-surface-hover);
  }

  .doc-review {
    font-size: 12px;
    color: var(--text-secondary);
    padding: 2px 6px;
    border-radius: var(--radius-sm);
    transition: color 0.1s, background 0.1s;
  }

  .doc-review:hover:not(:disabled) {
    color: var(--text-primary);
    background: var(--bg-surface-hover);
  }

  .doc-review:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }

  .doc-newtab {
    font-size: 12px;
    color: var(--text-secondary);
    text-decoration: none;
    padding: 2px 6px;
    border-radius: var(--radius-sm);
    transition: color 0.1s, background 0.1s;
  }

  .doc-newtab:hover {
    color: var(--text-primary);
    background: var(--bg-surface-hover);
    text-decoration: none;
  }

  .doc-body {
    flex: 1;
    overflow-y: auto;
    min-height: 0;
  }
</style>
