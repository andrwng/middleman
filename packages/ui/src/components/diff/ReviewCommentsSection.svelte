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

  // All published comments across all files, filtered to roots that
  // still resolve to a line in the current diff scope.
  const roots = $derived.by<PublishedReviewComment[]>(() => {
    const byPath = detailStore.getReviewCommentsByFilePath();
    const out: PublishedReviewComment[] = [];
    for (const list of byPath.values()) {
      for (const c of list) {
        if (c.inReplyTo === 0 && c.line > 0) out.push(c);
      }
    }
    // Stable order: by file path, then by line. Avoids the list
    // shuffling on every refresh.
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

  // Collapsed by default — section stays quiet on PRs without
  // posted review comments. Auto-expands the first time roots
  // appear so the reviewer notices new conversation.
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
    // Same scope-routing dance as QuestionsSection.scrollToThread:
    // if the comment's anchor commit is the current PR head, route
    // to HEAD scope (commit scope at head SHA shows head^..head,
    // not base..head, and the anchor's line numbers wouldn't
    // resolve).
    if (c.commitId) {
      const scope = diffStore.getScope();
      const commits = diffStore.getCommits();
      const isCurrentHead =
        commits && commits.length > 0 && commits[0]!.sha === c.commitId;
      const alreadyHeadHere = isCurrentHead && scope.kind === "head";
      const alreadyCommitHere =
        scope.kind === "commit" && scope.sha === c.commitId;
      if (!alreadyHeadHere && !alreadyCommitHere) {
        if (isCurrentHead) {
          await diffStore.resetToHead();
        } else {
          await diffStore.selectCommit(c.commitId);
        }
        await tick();
      }
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
            <button
              type="button"
              class="rc-item"
              onclick={() => void scrollToComment(c)}
              title="Jump to this comment in the diff"
            >
              <span class="rc-item__location">
                {c.path}
                <span class="rc-item__anchor">{anchorLabel(c)}</span>
              </span>
              <span class="rc-item__preview">{truncate(c.body, 80)}</span>
            </button>
          {/each}
        {/if}
        {#if others.length > 0}
          <div class="rc-subhead">Others</div>
          {#each others as c (c.id)}
            <button
              type="button"
              class="rc-item"
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
          {/each}
        {/if}
      </div>
    {/if}
  </div>
{/if}

<style>
  .rc-section {
    border-bottom: 1px solid var(--border-muted);
    background: var(--bg-surface);
  }

  .rc-section__toggle {
    display: flex;
    align-items: center;
    gap: 6px;
    width: 100%;
    padding: 8px 12px;
    background: transparent;
    border: 0;
    cursor: pointer;
    font: inherit;
    color: var(--text-primary);
    text-align: left;
  }
  .rc-section__toggle:hover {
    background: color-mix(in srgb, var(--text-primary) 5%, transparent);
  }

  .rc-section__chevron {
    display: inline-block;
    width: 12px;
    color: var(--text-muted);
    transition: transform 0.1s;
  }
  .rc-section__chevron--open {
    transform: rotate(90deg);
  }

  .rc-section__label {
    font-size: 12px;
    font-weight: 600;
  }

  .rc-section__count {
    font-size: 11px;
    color: var(--text-muted);
    background: color-mix(in srgb, var(--text-primary) 8%, transparent);
    border-radius: 999px;
    padding: 0 6px;
  }

  .rc-section__body {
    padding: 4px 4px 8px;
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
    display: grid;
    grid-template-columns: 1fr auto;
    gap: 2px 8px;
    width: 100%;
    padding: 6px 12px;
    background: transparent;
    border: 0;
    border-radius: var(--radius-sm);
    cursor: pointer;
    font: inherit;
    color: inherit;
    text-align: left;
  }
  .rc-item:hover {
    background: color-mix(in srgb, var(--text-primary) 6%, transparent);
  }
  .rc-item:focus-visible {
    outline: 2px solid var(--accent-blue);
    outline-offset: -2px;
  }

  .rc-item__location {
    grid-column: 1 / 2;
    font-family: var(--font-mono);
    font-size: 11px;
    color: var(--text-secondary);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    min-width: 0;
  }

  .rc-item__anchor {
    color: var(--text-muted);
    margin-left: 4px;
  }

  .rc-item__author {
    grid-column: 2 / 3;
    grid-row: 1 / 2;
    font-size: 10px;
    color: var(--text-muted);
    font-family: var(--font-mono);
  }

  .rc-item__preview {
    grid-column: 1 / -1;
    font-size: 12px;
    color: var(--text-primary);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
</style>
