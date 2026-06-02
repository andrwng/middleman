<script lang="ts">
  import { tick } from "svelte";
  import { getStores } from "../../context.js";
  import type { PublishedReviewComment } from "../../stores/detail.svelte.js";

  // Lists published top-of-thread review comments on this PR with
  // click-to-jump. "Top of thread" means `in_reply_to === 0` —
  // replies stay rolled up under their root in the diff itself.
  // Grouped into "Mine" and "Others" based on the viewer's GitHub
  // login. Outdated comments (server-side line === 0) are dropped
  // here; they're surfaced via the existing outdated-banner.

  const { detail: detailStore, diff: diffStore, viewer: viewerStore } = getStores();

  const roots = $derived.by<PublishedReviewComment[]>(() => {
    const byPath = detailStore.getReviewCommentsByFilePath();
    const out: PublishedReviewComment[] = [];
    for (const list of byPath.values()) {
      for (const c of list) {
        if (c.inReplyTo === 0 && c.line > 0) out.push(c);
      }
    }
    out.sort((a, b) => {
      if (a.path !== b.path) return a.path.localeCompare(b.path);
      return a.line - b.line;
    });
    return out;
  });

  const viewerLogin = $derived((viewerStore.getLogin() ?? "").toLowerCase());

  const mine = $derived(
    viewerLogin
      ? roots.filter((c) => c.author.toLowerCase() === viewerLogin)
      : [],
  );
  const others = $derived(
    viewerLogin
      ? roots.filter((c) => c.author.toLowerCase() !== viewerLogin)
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

  function anchorLabel(c: PublishedReviewComment): string {
    const side = c.side === "LEFT" ? "−" : "+";
    if (c.startLine != null && c.startLine !== c.line) {
      return `${side}${c.startLine}–${c.line}`;
    }
    return `${side}${c.line}`;
  }

  function truncate(text: string, n: number): string {
    const flat = text.replace(/\s+/g, " ").trim();
    if (flat.length <= n) return flat;
    return flat.slice(0, n).trimEnd() + "…";
  }

  async function scrollToComment(c: PublishedReviewComment): Promise<void> {
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
    const pr = diffStore.getCurrentPR();
    if (pr && diffStore.isFileCollapsed(pr.owner, pr.name, pr.number, c.path)) {
      diffStore.toggleFileCollapsed(pr.owner, pr.name, pr.number, c.path);
      await tick();
    }
    const selector =
      `.diff-file[data-file-path="${CSS.escape(c.path)}"] ` +
      `.line-wrap[data-anchor-line="${c.line}"]` +
      `[data-anchor-side="${c.side}"]`;
    const el = document.querySelector<HTMLElement>(selector);
    if (el) {
      el.scrollIntoView({ block: "center", behavior: "smooth" });
      el.classList.add("line-wrap--flash");
      window.setTimeout(() => el.classList.remove("line-wrap--flash"), 1500);
      return;
    }
    const fileEl = document.querySelector<HTMLElement>(
      `.diff-file[data-file-path="${CSS.escape(c.path)}"]`,
    );
    if (fileEl) {
      fileEl.scrollIntoView({ block: "start", behavior: "smooth" });
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
      <div class="rc-section__body">
        {#if mine.length > 0}
          <div class="rc-subhead">Mine</div>
          {#each mine as c (c.id)}
            <div class="rc-item">
              <button
                type="button"
                class="rc-item__main"
                onclick={() => void scrollToComment(c)}
                title="Jump to this comment in the diff"
              >
                <span class="rc-item__location">
                  {c.path}
                  <span class="rc-item__anchor">{anchorLabel(c)}</span>
                </span>
                <span class="rc-item__preview">{truncate(c.body, 80)}</span>
              </button>
            </div>
          {/each}
        {/if}
        {#if others.length > 0}
          <div class="rc-subhead">Others</div>
          {#each others as c (c.id)}
            <div class="rc-item">
              <button
                type="button"
                class="rc-item__main"
                onclick={() => void scrollToComment(c)}
                title="Jump to this comment in the diff"
              >
                <span class="rc-item__location">
                  {c.path}
                  <span class="rc-item__anchor">{anchorLabel(c)}</span>
                </span>
                <span class="rc-item__author">{c.author}</span>
                <span class="rc-item__preview">{truncate(c.body, 80)}</span>
              </button>
            </div>
          {/each}
        {/if}
      </div>
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
