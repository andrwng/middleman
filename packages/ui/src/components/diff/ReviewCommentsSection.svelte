<script lang="ts">
  import { tick } from "svelte";
  import { getStores } from "../../context.js";
  import type { PublishedReviewComment, ReviewCommentPlacement } from "../../stores/detail.svelte.js";
  import SectionResizeHandle from "./SectionResizeHandle.svelte";
  import { getSectionHeight } from "./sectionHeights.svelte.js";

  // Lists published top-of-thread review comments on this PR with
  // click-to-jump. "Top of thread" means `in_reply_to === 0` —
  // replies stay rolled up under their root in the diff itself.
  // Grouped into "Mine" and "Others" based on the viewer's GitHub
  // login. A comment with no line in the diff on screen is still listed,
  // badged, and jumps to the commit it was made on.

  const { detail: detailStore, diff: diffStore, viewer: viewerStore } = getStores();
  let bodyEl: HTMLDivElement | undefined = $state();

  // Reader-chosen height for this section's body. null leaves the
  // stylesheet's default cap in charge -- see sectionHeights for why
  // the cap is a max-height rather than a height.
  const bodyMax = $derived.by(() => {
    const h = getSectionHeight("review-comments");
    return h === null ? null : `${h}px`;
  });

  interface Entry {
    c: PublishedReviewComment;
    // Where the comment is drawn in the diff on screen, or null when it
    // has no line here.
    here: ReviewCommentPlacement | null;
    // What the list shows and sorts by: `here`, else the recorded anchor.
    at: ReviewCommentPlacement;
  }

  const roots = $derived.by<Entry[]>(() => {
    const byPath = detailStore.getReviewCommentsByFilePath();
    const out: Entry[] = [];
    for (const list of byPath.values()) {
      for (const c of list) {
        if (c.inReplyTo !== 0) continue;
        const here = detailStore.getReviewCommentPlacement(c);
        if (here === null && c.line <= 0) continue;
        out.push({ c, here, at: here ?? { path: c.path, line: c.line, startLine: c.startLine } });
      }
    }
    out.sort((a, b) => {
      if (a.at.path !== b.at.path) return a.at.path.localeCompare(b.at.path);
      return a.at.line - b.at.line;
    });
    return out;
  });

  const viewerLogin = $derived((viewerStore.getLogin() ?? "").toLowerCase());

  const mine = $derived(
    viewerLogin
      ? roots.filter((r) => r.c.author.toLowerCase() === viewerLogin)
      : [],
  );
  const others = $derived(
    viewerLogin
      ? roots.filter((r) => r.c.author.toLowerCase() !== viewerLogin)
      : roots,
  );

  let expanded = $state(false);
  let userCollapsed = $state(false);

  $effect(() => {
    if (roots.length > 0 && !userCollapsed) {
      expanded = true;
    }
  });

  function toggle(): void {
    expanded = !expanded;
    userCollapsed = !expanded;
  }

  function anchorLabel(e: Entry): string {
    const side = e.c.side === "LEFT" ? "−" : "+";
    const { line, startLine } = e.at;
    if (startLine != null && startLine !== line) {
      return `${side}${startLine}–${line}`;
    }
    return `${side}${line}`;
  }

  function driftLabel(e: Entry): string | null {
    if (e.here !== null) return null;
    const state = detailStore.getReviewCommentAnchor(e.c.id)?.state;
    return state === "removed" ? "line removed" : "position unknown";
  }

  function truncate(text: string, n: number): string {
    const flat = text.replace(/\s+/g, " ").trim();
    if (flat.length <= n) return flat;
    return flat.slice(0, n).trimEnd() + "…";
  }

  async function scrollToComment(e: Entry): Promise<void> {
    const { c, here, at } = e;
    // A comment with a line in the diff on screen is jumped to in place;
    // only one without falls back to the commit it was made on, where
    // its recorded line is valid.
    if (here === null) await routeToCommentCommit(c);
    const pr = diffStore.getCurrentPR();
    if (pr && diffStore.isFileCollapsed(pr.owner, pr.name, pr.number, at.path)) {
      diffStore.toggleFileCollapsed(pr.owner, pr.name, pr.number, at.path);
      await tick();
    }
    const selector =
      `.diff-file[data-file-path="${CSS.escape(at.path)}"] ` +
      `.line-wrap[data-anchor-line="${at.line}"]` +
      `[data-anchor-side="${c.side}"]`;
    const el = document.querySelector<HTMLElement>(selector);
    if (el) {
      el.scrollIntoView({ block: "center", behavior: "smooth" });
      el.classList.add("line-wrap--flash");
      window.setTimeout(() => el.classList.remove("line-wrap--flash"), 1500);
      return;
    }
    const fileEl = document.querySelector<HTMLElement>(
      `.diff-file[data-file-path="${CSS.escape(at.path)}"]`,
    );
    if (fileEl) {
      fileEl.scrollIntoView({ block: "start", behavior: "smooth" });
    }
  }

  async function routeToCommentCommit(c: PublishedReviewComment): Promise<void> {
    // Scope routing has three tiers:
    //   1. SHA is current head            → resetToHead (full base..head diff)
    //   2. SHA is a non-head PR commit    → selectCommit (per-commit diff)
    //   3. SHA isn't in current commits   → skip scope switch entirely
    //      (the comment was anchored to a force-pushed-away commit;
    //      calling selectCommit would hit a 400 from the server's
    //      "sha not in pull request" guard. Falling through with no
    //      switch is the least-bad option — the scroll may miss and
    //      the file-header fallback below will fire.)
    if (c.commitId) {
      const scope = diffStore.getScope();
      const commits = diffStore.getCommits();
      const isCurrentHead =
        commits && commits.length > 0 && commits[0]!.sha === c.commitId;
      const inCurrentCommits =
        commits?.some((cc) => cc.sha === c.commitId) ?? false;
      if (isCurrentHead) {
        if (scope.kind !== "head") {
          await diffStore.resetToHead();
          await tick();
        }
      } else if (inCurrentCommits) {
        if (scope.kind !== "commit" || scope.sha !== c.commitId) {
          await diffStore.selectCommit(c.commitId);
          await tick();
        }
      }
      // else: SHA is unreachable from current head; leave scope alone.
    }
  }
</script>

{#if roots.length > 0}
  <div class="rc-section">
    <div class="rc-section__header">
      <button class="rc-section__toggle" onclick={toggle}>
        <span class="rc-section__chevron" class:rc-section__chevron--open={expanded}>&#8250;</span>
        <span class="rc-section__label">Review comments</span>
        <span class="rc-section__count">{roots.length}</span>
      </button>
    </div>

    {#if expanded}
      <div class="rc-section__body" bind:this={bodyEl} style:max-height={bodyMax}>
        {#if mine.length > 0}
          <div class="rc-subhead">Mine</div>
          {#each mine as e (e.c.id)}
            <div class="rc-item">
              <button
                type="button"
                class="rc-item__main"
                onclick={() => void scrollToComment(e)}
                title="Jump to this comment in the diff"
              >
                <span class="rc-item__location">
                  {e.at.path}
                  <span class="rc-item__anchor">{anchorLabel(e)}</span>
                </span>
                {#if driftLabel(e)}
                  <span class="rc-item__drift">{driftLabel(e)}</span>
                {/if}
                <span class="rc-item__preview">{truncate(e.c.body, 80)}</span>
              </button>
            </div>
          {/each}
        {/if}
        {#if others.length > 0}
          <div class="rc-subhead">Others</div>
          {#each others as e (e.c.id)}
            <div class="rc-item">
              <button
                type="button"
                class="rc-item__main"
                onclick={() => void scrollToComment(e)}
                title="Jump to this comment in the diff"
              >
                <span class="rc-item__location">
                  {e.at.path}
                  <span class="rc-item__anchor">{anchorLabel(e)}</span>
                </span>
                {#if driftLabel(e)}
                  <span class="rc-item__drift">{driftLabel(e)}</span>
                {/if}
                <span class="rc-item__author">{e.c.author}</span>
                <span class="rc-item__preview">{truncate(e.c.body, 80)}</span>
              </button>
            </div>
          {/each}
        {/if}
      </div>
      <SectionResizeHandle id="review-comments" body={bodyEl} label="Resize Review comments" />
    {/if}
  </div>
{/if}

<style>
  /* Mirrors QuestionsSection styling so the sidebar reads as a
     coherent stack of sections. Only difference: a subhead row
     between "Mine" and "Others" subgroups. */
  .rc-section {
    background: var(--bg-inset);
    border-bottom: 1px solid var(--diff-border);
  }

  .rc-section__header {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 2px 10px 2px 0;
  }

  .rc-section__toggle {
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
  .rc-section__toggle:hover {
    background: var(--bg-surface-hover);
  }

  .rc-section__chevron {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    font-size: 12px;
    width: 12px;
    height: 12px;
    color: var(--text-muted);
    transition: transform 0.15s;
  }
  .rc-section__chevron--open {
    transform: rotate(90deg);
  }

  .rc-section__label {
    font-size: 11px;
    color: var(--text-muted);
    text-transform: uppercase;
    letter-spacing: 0.4px;
  }

  .rc-section__count {
    font-size: 10px;
    font-family: var(--font-mono);
    color: var(--text-muted);
    background: var(--diff-bg);
    border: 1px solid var(--diff-border);
    border-radius: 999px;
    padding: 1px 6px;
  }

  .rc-section__body {
    padding: 2px 0 4px;
    max-height: 40vh;
    overflow-y: auto;
  }

  .rc-subhead {
    padding: 6px 12px 2px;
    font-size: 10px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--text-muted);
  }

  .rc-item {
    display: flex;
    align-items: stretch;
    padding: 4px 10px 4px 12px;
    gap: 4px;
  }
  .rc-item:hover {
    background: var(--bg-surface-hover);
  }

  .rc-item__main {
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

  .rc-item__location {
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--text-secondary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    flex: 0 1 auto;
    min-width: 0;
  }

  .rc-item__anchor {
    color: var(--text-muted);
    margin-left: 3px;
  }

  .rc-item__drift {
    font-size: 9px;
    color: var(--accent-amber);
    flex-shrink: 0;
    white-space: nowrap;
  }

  .rc-item__author {
    font-size: 10px;
    font-family: var(--font-mono);
    color: var(--text-muted);
    flex-shrink: 0;
  }

  .rc-item__preview {
    font-size: 11px;
    color: var(--text-muted);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    flex: 1 1 auto;
    min-width: 0;
  }
</style>
