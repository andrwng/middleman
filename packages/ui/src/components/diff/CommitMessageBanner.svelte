<script lang="ts">
  import { getStores } from "../../context.js";
  import { renderMarkdown } from "../../utils/markdown.js";

  // Pinned commit-message banner for the Review surface. Mirrors
  // ReviewCoverBanner's pattern: a chevron-toggleable header with a
  // collapsible body, persisted across PRs/sessions in localStorage.
  // Renders only when the diff scope is a single commit — that's the
  // only state where a commit-level subject/body is meaningful.
  //
  // Lives outside the diff scroll area so it stays visible while the
  // reviewer scrolls the diff.

  interface Props {
    owner: string;
    name: string;
    number: number;
    // Render the body regardless of the persisted `collapsed` flag.
    // Used by the consolidated top-sections "peek" flow so a previously
    // collapsed banner still reveals its body when the user clicks the
    // pip. Defaults to false so stacked banners honor localStorage.
    forceExpanded?: boolean;
  }

  const { owner, name, number, forceExpanded = false }: Props = $props();

  const { diff: diffStore, commitAnalysis: caStore } = getStores();

  const scope = $derived(diffStore.getScope());
  const activeCommit = $derived(diffStore.getActiveCommit());
  const commitIndex = $derived(diffStore.getCommitIndex());
  const visible = $derived(
    scope.kind === "commit" && activeCommit !== null && commitIndex !== null,
  );

  let collapsed = $state(
    typeof localStorage !== "undefined" &&
      localStorage.getItem("pr-commit-msg-collapsed") === "true",
  );
  // Effective collapsed state: persisted collapse is overridden by the
  // peek-flow's forceExpanded prop. The chevron rotation and title
  // text must reflect what's actually visible — otherwise a peeked
  // banner shows a "collapsed" chevron over an expanded body.
  const effectivelyCollapsed = $derived(collapsed && !forceExpanded);
  function toggle(): void {
    collapsed = !collapsed;
    try {
      localStorage.setItem("pr-commit-msg-collapsed", String(collapsed));
    } catch {
      /* ignore */
    }
  }

  // Bind the analysis store to this PR and lazy-fetch whenever the
  // active commit changes. The store handles its own polling for
  // in-flight runs.
  $effect(() => {
    caStore.setPR(owner, name, number);
  });
  $effect(() => {
    if (activeCommit?.sha) {
      void caStore.fetchFor(activeCommit.sha);
    }
  });

  const analysis = $derived(
    activeCommit?.sha ? caStore.get(activeCommit.sha) : null,
  );
  const analyzing = $derived(
    activeCommit?.sha ? caStore.isInFlight(activeCommit.sha) : false,
  );

  async function analyze(): Promise<void> {
    if (!activeCommit?.sha) return;
    await caStore.generate(activeCommit.sha);
  }

  async function clearAnalysis(): Promise<void> {
    if (!activeCommit?.sha) return;
    await caStore.remove(activeCommit.sha);
  }
</script>

{#if visible && activeCommit && commitIndex}
  <div class="commit-banner" class:commit-banner--collapsed={effectivelyCollapsed}>
    <button
      type="button"
      class="commit-banner__toggle"
      onclick={toggle}
      title={effectivelyCollapsed ? "Expand commit message" : "Collapse commit message"}
    >
      <svg
        class="commit-banner__chevron"
        class:commit-banner__chevron--collapsed={effectivelyCollapsed}
        width="10" height="10" viewBox="0 0 10 10" fill="none"
        stroke="currentColor" stroke-width="1.6"
      >
        <polyline points="2,3.5 5,6.5 8,3.5" stroke-linecap="round" stroke-linejoin="round" />
      </svg>
      <span class="commit-banner__crumb commit-banner__crumb--pr">PR #{number}</span>
      <span class="commit-banner__sep">&rsaquo;</span>
      <span class="commit-banner__crumb commit-banner__crumb--pos">
        Commit {commitIndex.current}/{commitIndex.total}
      </span>
      <span class="commit-banner__sep">&rsaquo;</span>
      <span class="commit-banner__crumb commit-banner__crumb--sha">
        {activeCommit.sha.slice(0, 7)}
      </span>
      <span class="commit-banner__sep">&rsaquo;</span>
      <span class="commit-banner__subject">{activeCommit.message}</span>
      <span class="commit-banner__author">{activeCommit.author_name}</span>
    </button>
    {#if (!collapsed || forceExpanded) && activeCommit.body}
      <div class="commit-banner__body">{activeCommit.body}</div>
    {/if}
    {#if !collapsed || forceExpanded}
      <div class="commit-banner__analysis">
        <div class="commit-banner__analysis-controls">
          {#if !analysis && !analyzing}
            <button
              type="button"
              class="commit-banner__btn"
              onclick={analyze}
              title="Have Claude write a short guide for reviewing this commit"
            >
              Analyze commit
            </button>
          {:else if analyzing}
            <span class="commit-banner__analyzing">Analyzing…</span>
          {:else if analysis}
            <span class="commit-banner__analysis-meta">Guide for {activeCommit.sha.slice(0, 7)}</span>
            <button
              type="button"
              class="commit-banner__btn commit-banner__btn--ghost"
              onclick={analyze}
              title="Regenerate the analysis from scratch"
            >
              Re-analyze
            </button>
            <button
              type="button"
              class="commit-banner__btn commit-banner__btn--ghost"
              onclick={clearAnalysis}
              title="Discard the cached analysis"
            >
              Clear
            </button>
          {/if}
        </div>
        {#if analysis && analysis.status === "failed"}
          <div class="commit-banner__analysis-error">
            Analysis failed: {analysis.error || "unknown error"}
          </div>
        {:else if analysis && analysis.status === "done" && analysis.content}
          <div class="commit-banner__analysis-body markdown-body">
            {@html renderMarkdown(analysis.content, { owner, name, sha: activeCommit.sha })}
          </div>
        {/if}
      </div>
    {/if}
  </div>
{/if}

<style>
  .commit-banner {
    display: flex;
    flex-direction: column;
    border-bottom: 1px solid var(--diff-border);
    background: var(--bg-inset);
    flex-shrink: 0;
  }

  .commit-banner--collapsed {
    border-bottom-color: var(--border-muted);
  }

  .commit-banner__toggle {
    display: flex;
    align-items: baseline;
    gap: 6px;
    padding: 8px 16px;
    width: 100%;
    border: none;
    background: none;
    text-align: left;
    cursor: pointer;
    color: var(--text-muted);
    font-size: 11px;
    min-width: 0;
  }

  .commit-banner__toggle:hover {
    background: var(--bg-surface-hover);
  }

  .commit-banner__chevron {
    flex-shrink: 0;
    transition: transform 0.15s;
    color: var(--text-muted);
    align-self: center;
  }

  .commit-banner__chevron--collapsed {
    transform: rotate(-90deg);
  }

  .commit-banner__crumb {
    min-width: 0;
    flex-shrink: 0;
  }

  .commit-banner__crumb--pr {
    font-weight: 600;
    color: var(--text-secondary);
  }

  .commit-banner__crumb--pos {
    font-weight: 600;
    color: var(--accent-blue);
  }

  .commit-banner__crumb--sha {
    font-family: var(--font-mono);
    font-size: 10px;
    /* Selectable inside the toggle button so the SHA can be copied. */
    user-select: text;
  }

  .commit-banner__sep {
    color: var(--text-muted);
    font-size: 12px;
    user-select: none;
    flex-shrink: 0;
  }

  .commit-banner__subject {
    font-size: 13px;
    color: var(--text-primary);
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .commit-banner__author {
    margin-left: auto;
    color: var(--text-muted);
    flex-shrink: 0;
  }

  .commit-banner__body {
    padding: 4px 16px 14px 34px;
    max-height: 30vh;
    overflow-y: auto;
    font-family: var(--font-mono);
    font-size: 12px;
    color: var(--text-secondary);
    white-space: pre-wrap;
    word-break: break-word;
    line-height: 1.5;
  }

  /* Per-commit analysis surface — control row + rendered markdown
     guide. Sits below the commit body, inside the same pinned
     banner so it travels with the commit context. */
  .commit-banner__analysis {
    padding: 0 16px 12px 34px;
    border-top: 1px dashed var(--border-muted);
    margin-top: 4px;
  }

  .commit-banner__analysis-controls {
    display: flex;
    gap: 8px;
    align-items: center;
    padding: 8px 0 4px;
    font-size: 11px;
    color: var(--text-muted);
  }

  .commit-banner__analysis-meta {
    font-family: var(--font-mono);
    color: var(--text-muted);
    margin-right: auto;
  }

  .commit-banner__btn {
    padding: 3px 10px;
    font-size: 11px;
    font-weight: 600;
    color: #fff;
    background: var(--accent-claude);
    border: 1px solid var(--accent-claude);
    border-radius: var(--radius-sm);
    cursor: pointer;
  }
  .commit-banner__btn:hover {
    filter: brightness(1.08);
  }

  .commit-banner__btn--ghost {
    color: var(--text-secondary);
    background: transparent;
    border-color: var(--border-muted);
  }

  .commit-banner__analyzing {
    font-style: italic;
    color: var(--accent-claude);
  }

  .commit-banner__analysis-error {
    margin-top: 4px;
    color: var(--accent-red);
    font-size: 11px;
  }

  .commit-banner__analysis-body {
    margin-top: 6px;
    max-height: 50vh;
    overflow-y: auto;
    font-size: 13px;
    line-height: 1.5;
    color: var(--text-primary);
  }

  /* Cap prose width inside the analysis body (same rule as the AI
     brief): only prose elements; code blocks / tables stay full
     width so ASCII Layers diagrams and code excerpts can breathe. */
  .commit-banner__analysis-body :global(p),
  .commit-banner__analysis-body :global(ul),
  .commit-banner__analysis-body :global(ol),
  .commit-banner__analysis-body :global(blockquote),
  .commit-banner__analysis-body :global(h1),
  .commit-banner__analysis-body :global(h2),
  .commit-banner__analysis-body :global(h3),
  .commit-banner__analysis-body :global(h4),
  .commit-banner__analysis-body :global(h5),
  .commit-banner__analysis-body :global(h6) {
    max-width: 80ch;
  }
  .commit-banner__analysis-body :global(pre) {
    overflow-x: auto;
  }
  .commit-banner__analysis-body :global(code) {
    word-break: keep-all;
    overflow-wrap: normal;
  }
</style>
