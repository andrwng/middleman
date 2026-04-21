<script lang="ts">
  import { getStores } from "../../context.js";
  import type { DraftComment } from "../../stores/diff.svelte.js";

  const { diff: diffStore } = getStores();

  const comments = $derived(diffStore.getDraft().comments);
  const commits = $derived(diffStore.getCommits());

  // Head SHA we'll publish against. Falls back to empty when the
  // commit list hasn't loaded yet — we skip drift flagging in that
  // window to avoid flapping.
  const headSha = $derived(commits && commits.length > 0 ? commits[0]!.sha : "");

  // Drifted = anchored to a commit that isn't what we'll publish
  // against now. An empty commit_sha is also drifted: the draft was
  // made before we had a commit list so its effective anchor is
  // unknown and may not resolve.
  function isDrifted(c: DraftComment): boolean {
    return headSha !== "" && (c.commitSha === "" || c.commitSha !== headSha);
  }

  function shaLabel(c: DraftComment): string {
    return c.commitSha ? c.commitSha.slice(0, 7) : "???";
  }

  // Collapsed by default; expands automatically on first draft so
  // the reviewer sees their pending pile once they add to it.
  let expanded = $state(false);
  let userCollapsed = $state(false);

  $effect(() => {
    if (comments.length > 0 && !userCollapsed) {
      expanded = true;
    }
  });

  function toggle(): void {
    expanded = !expanded;
    userCollapsed = !expanded;
  }

  function anchorLabel(c: DraftComment): string {
    const sign = c.side === "LEFT" ? "−" : "+";
    if (c.startLine != null && c.startLine !== c.line) {
      return `${sign}${c.startLine}–${c.line}`;
    }
    return `${sign}${c.line}`;
  }

  function scrollToDraft(c: DraftComment): void {
    // Multi-line drafts anchor to the end line (GitHub convention);
    // the pending card is rendered right after the anchor line-wrap
    // in DiffFile, so scrolling to the anchor puts the card in view.
    const selector =
      `.diff-file[data-file-path="${CSS.escape(c.path)}"] ` +
      `.line-wrap[data-anchor-line="${c.line}"]` +
      `[data-anchor-side="${c.side}"]`;
    const el = document.querySelector<HTMLElement>(selector);
    if (el) {
      el.scrollIntoView({ block: "center", behavior: "smooth" });
    }
  }

  function remove(c: DraftComment): void {
    diffStore.removeDraftComment(c.id);
  }

  function truncate(text: string, n: number): string {
    if (text.length <= n) return text;
    return text.slice(0, n).trimEnd() + "…";
  }

  const driftedCount = $derived(comments.filter(isDrifted).length);
</script>

{#if comments.length > 0}
  <div class="drafts-section">
    <div class="drafts-section__header">
      <button class="drafts-section__toggle" onclick={toggle}>
        <span class="drafts-section__chevron" class:drafts-section__chevron--open={expanded}>&#8250;</span>
        <span class="drafts-section__label">Drafts</span>
        <span class="drafts-section__count">{comments.length}</span>
        {#if driftedCount > 0}
          <span class="drafts-section__drift" title="{driftedCount} draft{driftedCount === 1 ? "" : "s"} anchored to an older commit — may fail on publish">
            {driftedCount} drifted
          </span>
        {/if}
      </button>
    </div>

    {#if expanded}
      <div class="drafts-section__body">
        {#each comments as c (c.id)}
          <div class="draft-item" class:draft-item--drifted={isDrifted(c)}>
            <button
              type="button"
              class="draft-item__main"
              onclick={() => scrollToDraft(c)}
              title="Scroll to this draft in the diff"
            >
              <span class="draft-item__anchor">{anchorLabel(c)}</span>
              <span
                class="draft-item__sha"
                class:draft-item__sha--drifted={isDrifted(c)}
                title={c.commitSha
                  ? `Anchored to ${shaLabel(c)}`
                  : "Anchor commit unknown (drafted before the commit list loaded)"}
              >
                @ {shaLabel(c)}
              </span>
              <span class="draft-item__path">{c.path}</span>
              <span class="draft-item__preview">{truncate(c.body, 80)}</span>
            </button>
            <button
              type="button"
              class="draft-item__delete"
              title="Delete this draft"
              onclick={() => remove(c)}
            >
              <svg width="10" height="10" viewBox="0 0 10 10" fill="none" stroke="currentColor" stroke-width="1.6">
                <path d="M2 2L8 8M8 2L2 8" stroke-linecap="round" />
              </svg>
            </button>
          </div>
        {/each}
      </div>
    {/if}
  </div>
{/if}

<style>
  .drafts-section {
    background: var(--bg-inset);
    border-bottom: 1px solid var(--diff-border);
  }

  .drafts-section__header {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 2px 10px 2px 0;
  }

  .drafts-section__toggle {
    display: flex;
    align-items: center;
    gap: 6px;
    flex: 1;
    min-width: 0;
    padding: 4px 6px 4px 10px;
    border: none;
    background: none;
    cursor: pointer;
    text-align: left;
    color: var(--text-primary);
    border-radius: var(--radius-sm);
  }

  .drafts-section__toggle:hover {
    background: var(--bg-surface-hover);
  }

  .drafts-section__chevron {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    font-size: 12px;
    width: 12px;
    height: 12px;
    color: var(--text-muted);
    transition: transform 0.15s;
  }

  .drafts-section__chevron--open {
    transform: rotate(90deg);
  }

  .drafts-section__label {
    font-size: 11px;
    color: var(--text-muted);
    text-transform: uppercase;
    letter-spacing: 0.4px;
  }

  .drafts-section__count {
    font-size: 10px;
    font-family: var(--font-mono);
    color: var(--text-muted);
    background: var(--diff-bg);
    border: 1px solid var(--diff-border);
    border-radius: 999px;
    padding: 1px 6px;
  }

  .drafts-section__drift {
    margin-left: auto;
    font-size: 10px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--accent-amber);
  }

  .drafts-section__body {
    padding: 2px 0 4px;
    max-height: 40vh;
    overflow-y: auto;
  }

  .draft-item {
    display: flex;
    align-items: center;
    gap: 4px;
    padding: 4px 10px 4px 12px;
  }

  .draft-item:hover {
    background: var(--bg-surface-hover);
  }

  .draft-item__main {
    display: flex;
    align-items: center;
    gap: 6px;
    flex: 1;
    min-width: 0;
    border: none;
    background: none;
    text-align: left;
    cursor: pointer;
    padding: 0;
    color: inherit;
  }

  .draft-item__anchor {
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--accent-blue);
    background: color-mix(in srgb, var(--accent-blue) 14%, transparent);
    padding: 1px 6px;
    border-radius: 999px;
    flex-shrink: 0;
  }

  .draft-item--drifted .draft-item__anchor {
    color: var(--accent-amber);
    background: color-mix(in srgb, var(--accent-amber) 14%, transparent);
  }

  .draft-item__sha {
    font-family: var(--font-mono);
    font-size: 10px;
    color: var(--text-muted);
    padding: 1px 6px;
    border-radius: 999px;
    border: 1px solid var(--border-muted);
    background: var(--bg-inset);
    flex-shrink: 0;
    cursor: help;
  }

  .draft-item__sha--drifted {
    color: var(--accent-amber);
    border-color: var(--accent-amber);
    background: color-mix(in srgb, var(--accent-amber) 12%, var(--bg-inset));
  }

  .draft-item__path {
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--text-secondary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    flex: 0 1 auto;
    min-width: 0;
  }

  .draft-item__preview {
    font-size: 11px;
    color: var(--text-muted);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    flex: 1 1 auto;
    min-width: 0;
  }

  .draft-item__delete {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 18px;
    height: 18px;
    border: none;
    border-radius: var(--radius-sm);
    background: none;
    color: var(--text-muted);
    cursor: pointer;
    flex-shrink: 0;
  }

  .draft-item__delete:hover {
    background: var(--bg-surface-hover);
    color: var(--accent-red);
  }
</style>
