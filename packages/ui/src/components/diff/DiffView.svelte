<script lang="ts">
  import { onMount } from "svelte";
  import { getStores } from "../../context.js";

  const { diff: diffStore, ai: aiStore, brief: briefStore, reviewThreads: reviewThreadsStore } = getStores();
  import DiffToolbar from "./DiffToolbar.svelte";
  import DiffFileComponent from "./DiffFile.svelte";
  import ReviewPanel from "./ReviewPanel.svelte";

  interface Props {
    owner: string;
    name: string;
    number: number;
  }

  const { owner, name, number }: Props = $props();

  let diffArea: HTMLDivElement | undefined = $state();
  let scrollRaf = 0;
  let reviewPanelOpen = $state(false);

  onMount(() => {
    void diffStore.loadDiff(owner, name, number);
    // Preload commits so currentCommitSha() always has the head to
    // anchor new drafts against. Otherwise drafts made before the
    // Commits panel is expanded carry commitSha = "" and render as
    // "on current" (blue) when they're really unknown.
    void diffStore.loadCommits();
    aiStore.start(owner, name, number);
    void reviewThreadsStore.load(owner, name, number);
    briefStore.start(owner, name, number);

    return () => {
      cancelAnimationFrame(scrollRaf);
      diffStore.clearDiff();
      aiStore.stop();
      reviewThreadsStore.clear();
      briefStore.stop();
    };
  });

  const diff = $derived(diffStore.getDiff());
  const loading = $derived(diffStore.isDiffLoading());
  const error = $derived(diffStore.getDiffError());
  const tabWidth = $derived(diffStore.getTabWidth());
  const scope = $derived(diffStore.getScope());
  const interdiff = $derived(diffStore.getInterdiff());

  function scrollToFile(path: string): void {
    if (!diffArea) return;
    const el = diffArea.querySelector(`[data-file-path="${CSS.escape(path)}"]`);
    if (el) {
      el.scrollIntoView({ behavior: "instant", block: "start" });
    }
    // Clear the scrolling flag after the instant scroll so the next user-initiated
    // scroll event resumes active file tracking.
    scrollRaf = requestAnimationFrame(() => diffStore.clearScrolling());
  }

  // Watch for scroll requests from the sidebar file list (via the store).
  // Only consume the target once diffArea is mounted and diff data is available,
  // so the request is not lost if the user clicks a file before diff renders.
  $effect(() => {
    const target = diffStore.getScrollTarget();
    if (target && diffArea && diff) {
      diffStore.consumeScrollTarget();
      scrollToFile(target);
    }
  });

  // Scroll-based active file tracking.
  // Skipped for one frame after programmatic scroll to avoid re-setting activeFile.
  function onDiffScroll(): void {
    if (!diffArea || !diff) return;
    if (diffStore.isScrolling()) return;
    const rect = diffArea.getBoundingClientRect();
    const threshold = rect.top + 60;

    let current: string | null = null;
    for (const file of diff.files) {
      const el = diffArea.querySelector(`[data-file-path="${CSS.escape(file.path)}"]`);
      if (!el) continue;
      const elRect = el.getBoundingClientRect();
      if (elRect.top <= threshold) {
        current = file.path;
      }
    }
    if (current !== null) {
      diffStore.setActiveFile(current);
    }
  }

  // j/k keyboard navigation between files.
  function handleKeydown(e: KeyboardEvent): void {
    const tag = (e.target as HTMLElement).tagName;
    if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return;
    if ((e.target as HTMLElement).isContentEditable) return;
    // Unmodified single-letter shortcuts only — don't swallow
    // Cmd/Ctrl-F, Cmd-J (downloads), etc.
    if (e.metaKey || e.ctrlKey || e.altKey) return;

    if (e.key === "j" || e.key === "k") {
      if (!diff || diff.files.length === 0) return;
      e.preventDefault();
      const paths = diff.files.map((f) => f.path);
      const currentIdx = diffStore.getActiveFile() ? paths.indexOf(diffStore.getActiveFile()!) : -1;
      let nextIdx: number;
      if (e.key === "j") {
        nextIdx = currentIdx < paths.length - 1 ? currentIdx + 1 : currentIdx;
      } else {
        nextIdx = currentIdx > 0 ? currentIdx - 1 : 0;
      }
      const nextPath = paths[nextIdx] ?? null;
      if (nextPath) diffStore.requestScrollToFile(nextPath);
    }

    if (e.key === "[" || e.key === "]") {
      e.preventDefault();
      if (e.key === "[") {
        diffStore.stepPrev();
      } else {
        diffStore.stepNext();
      }
    }

    if (e.key === "m") {
      e.preventDefault();
      diffStore.jumpToNextUnreviewed();
    }
  }

  $effect(() => {
    window.addEventListener("keydown", handleKeydown);
    return () => window.removeEventListener("keydown", handleKeydown);
  });

  // Auto-mark commit as reviewed when its diff finishes loading
  $effect(() => {
    if (scope.kind === "commit" && diff && !loading) {
      diffStore.markCommitReviewed(scope.sha);
    }
  });
</script>

<div class="diff-view">
  {#if diff?.stale}
    <div class="stale-banner">
      Diff may be outdated -- showing changes as of an earlier version of this PR.
    </div>
  {/if}
  {#if interdiff && interdiff.kind !== "clean"}
    <div class="interdiff-banner" role="status">
      <strong>
        {#if interdiff.kind === "conflicted"}
          Interdiff unavailable — rebase noise could not be subtracted.
        {:else}
          Interdiff unavailable — patchsets have no common ancestor.
        {/if}
      </strong>
      <span class="interdiff-banner__detail">
        {#if interdiff.kind === "conflicted"}
          Showing only files the author touched in the new patchset. The diff
          for each may still include changes from the rebase itself.
        {:else}
          Showing the raw diff between patchset heads.{interdiff.reason ? ` (${interdiff.reason})` : ""}
        {/if}
      </span>
    </div>
  {/if}

  <div class="diff-body">
    {#if loading && !diff}
      <div class="diff-state">
        <svg class="diff-spinner" width="20" height="20" viewBox="0 0 20 20" fill="none">
          <circle cx="10" cy="10" r="8" stroke="currentColor" stroke-opacity="0.2" stroke-width="2" />
          <path d="M18 10a8 8 0 0 0-8-8" stroke="currentColor" stroke-width="2" stroke-linecap="round" />
        </svg>
        <p class="diff-state-msg">Loading diff</p>
      </div>
    {:else if error}
      <div class="diff-state">
        <p class="diff-state-msg diff-state-msg--error">{error}</p>
      </div>
    {:else if diff}
      <div class="diff-main">
        <DiffToolbar onReviewClick={() => { reviewPanelOpen = true; }} />
        <div
          class="diff-area"
          bind:this={diffArea}
          onscroll={onDiffScroll}
          style:tab-size={tabWidth}
        >
          {#each diff.files as file (file.path)}
            <DiffFileComponent
              {file}
              {owner}
              {name}
              {number}
            />
          {/each}
        </div>
      </div>
    {/if}
  </div>
</div>

{#if reviewPanelOpen}
  <ReviewPanel {owner} {name} {number} onclose={() => { reviewPanelOpen = false; }} />
{/if}

<style>
  .diff-view {
    display: flex;
    flex-direction: column;
    flex: 1;
    overflow: hidden;
    background: var(--diff-bg);
  }

  .stale-banner {
    padding: 6px 16px;
    background: var(--diff-stale-bg);
    color: var(--diff-stale-text);
    border-bottom: 1px solid var(--diff-stale-border);
    font-size: 12px;
    flex-shrink: 0;
  }

  .interdiff-banner {
    padding: 6px 16px;
    background: color-mix(in srgb, var(--accent-amber) 15%, var(--bg-surface));
    color: var(--text-primary);
    border-bottom: 1px solid var(--accent-amber);
    font-size: 12px;
    flex-shrink: 0;
    display: flex;
    gap: 8px;
    align-items: baseline;
    flex-wrap: wrap;
  }

  .interdiff-banner__detail {
    color: var(--text-secondary);
  }

  .diff-body {
    display: flex;
    flex: 1;
    overflow: hidden;
  }

  .diff-main {
    display: flex;
    flex-direction: column;
    flex: 1;
    min-width: 0;
    overflow: hidden;
  }

  .diff-area {
    flex: 1;
    overflow: auto;
  }

  .diff-state {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 8px;
    flex: 1;
  }

  .diff-spinner {
    animation: spin 0.8s linear infinite;
    color: var(--text-muted);
  }

  .diff-state-msg {
    font-size: 13px;
    color: var(--text-muted);
  }

  .diff-state-msg--error {
    color: var(--accent-red);
  }

  @keyframes spin {
    to { transform: rotate(360deg); }
  }
</style>
