<script lang="ts">
  import { onMount } from "svelte";
  import RenderedMarkdownView from "../diff/RenderedMarkdownView.svelte";
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
</script>

<div class="doc-surface">
  <div class="doc-header">
    <button class="doc-back" onclick={() => navigate(reviewRoute)}>
      ← Review
    </button>
    <span class="doc-path">{path}</span>
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
      commentLayout="gutter"
    />
  </div>
</div>

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
