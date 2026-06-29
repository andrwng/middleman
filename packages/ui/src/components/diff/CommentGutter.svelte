<script lang="ts" module>
  import type { DraftComment } from "../../stores/diff.svelte.js";
  import type { PublishedReviewComment } from "../../stores/detail.svelte.js";
  import type { AIThread } from "../../stores/ai.svelte.js";

  export type CardSpec =
    | { kind: "draft"; key: string; comment: DraftComment }
    | { kind: "published"; key: string; comment: PublishedReviewComment }
    | { kind: "ai"; key: string; thread: AIThread };

  interface Anchor {
    line: number;
    side: "LEFT" | "RIGHT";
    startLine?: number;
  }

  // GutterEntry is a discriminated union:
  //   "cards"        — a stacked block of review cards (drafts, published, AI threads)
  //   "composer-diff"— a DiffComposer rendered at the given offset
  //   "composer-ask" — an AIAskComposer rendered at the given offset
  export type GutterEntry =
    | { kind: "cards"; key: string; desiredTop: number; cards: CardSpec[] }
    | {
        kind: "composer-diff";
        key: string;
        desiredTop: number;
        anchor: Anchor;
        onsave: (body: string) => void;
        oncancel: () => void;
      }
    | {
        kind: "composer-ask";
        key: string;
        desiredTop: number;
        anchor: Anchor;
        error: string | null;
        submitting: boolean;
        onsubmit: (question: string) => void;
        oncancel: () => void;
      };
</script>

<script lang="ts">
  import { resolveStack } from "./gutterStack.js";
  import AIThreadCard from "./AIThreadCard.svelte";
  import ReviewCommentCard from "./ReviewCommentCard.svelte";
  import PendingCommentCard from "./PendingCommentCard.svelte";
  import DiffComposer from "./DiffComposer.svelte";
  import AIAskComposer from "./AIAskComposer.svelte";

  interface Props {
    entries: GutterEntry[];
    repoOwner: string;
    repoName: string;
    currentHeadSha: string;
    // Key of the entry to highlight (hover cross-link with its source block).
    highlightedKey: string | null;
    // Report hover enter/leave so the parent can highlight the source block.
    onhighlight: (key: string | null) => void;
    // Jump to a card's source block (the per-card "scroll to source" button).
    onactivate: (key: string) => void;
    ondelete: (id: string) => void;
  }

  const {
    entries,
    repoOwner,
    repoName,
    currentHeadSha,
    highlightedKey,
    onhighlight,
    onactivate,
    ondelete,
  }: Props = $props();

  // Resolved top offsets, one per entry (index-aligned). The effect sets these
  // before visibility is revealed, so wrappers are never painted at incorrect
  // positions. Start empty; the effect fills them on first run.
  let resolvedTops = $state<number[]>([]);

  // Bound wrapper elements, index-aligned with entries.
  let wrapperEls = $state<(HTMLElement | undefined)[]>([]);

  // The gutter container; hover cross-linking is delegated to it (below) rather
  // than attaching per-entry markup handlers, which keeps the entry divs free
  // of static-interaction a11y handlers.
  let gutterEl: HTMLElement | undefined = $state();

  // Track visibility: false until the first resolveStack pass completes so
  // wrappers are never painted at incorrect positions.
  let visible = $state(false);

  // Re-run resolveStack whenever entries or measured heights change.
  $effect(() => {
    // Iterate entries inside the effect so Svelte tracks it as a reactive dep.
    const items = entries.map((e, i) => {
      const el = wrapperEls[i];
      const height = el ? el.offsetHeight : 0;
      return { desiredTop: e.desiredTop, height };
    });
    resolvedTops = resolveStack(items, 8);
    visible = true;
  });

  // Hover cross-link: delegate mouseover/mouseleave on the container so the
  // parent can highlight the source block of whichever cards entry is hovered.
  // onhighlight is read only inside the listeners (at call time), so this effect
  // tracks gutterEl alone and attaches once. Composer entries are ignored.
  $effect(() => {
    const el = gutterEl;
    if (!el) return;
    const over = (ev: Event) => {
      const entry = (ev.target as HTMLElement).closest<HTMLElement>(
        ".comment-gutter__entry[data-gutter-key]",
      );
      const key = entry?.dataset.gutterKey ?? null;
      onhighlight(key && key.startsWith("block:") ? key : null);
    };
    const leave = () => onhighlight(null);
    el.addEventListener("mouseover", over);
    el.addEventListener("mouseleave", leave);
    return () => {
      el.removeEventListener("mouseover", over);
      el.removeEventListener("mouseleave", leave);
    };
  });
</script>

<div class="comment-gutter" bind:this={gutterEl}>
  {#each entries as e, i (e.key)}
    <div
      class="comment-gutter__entry"
      class:comment-gutter__entry--hidden={!visible}
      class:comment-gutter__entry--linked={e.kind === "cards" && e.key === highlightedKey}
      data-gutter-key={e.key}
      style:top="{resolvedTops[i] ?? e.desiredTop}px"
      bind:this={wrapperEls[i]}
    >
      {#if e.kind === "composer-diff"}
        <DiffComposer anchor={e.anchor} onsave={e.onsave} oncancel={e.oncancel} />
      {:else if e.kind === "composer-ask"}
        <AIAskComposer
          anchor={e.anchor}
          error={e.error}
          submitting={e.submitting}
          onsubmit={e.onsubmit}
          oncancel={e.oncancel}
        />
      {:else}
        <button
          class="comment-gutter__jump"
          title="Scroll to the commented text"
          aria-label="Scroll to the commented text"
          onclick={() => onactivate(e.key)}>⤴</button>
        {#each e.cards as spec (spec.key)}
          {#if spec.kind === "ai"}
            <AIThreadCard thread={spec.thread} {repoOwner} repoName={repoName} />
          {:else if spec.kind === "published"}
            <ReviewCommentCard
              comment={spec.comment}
              {repoOwner}
              repoName={repoName}
              {currentHeadSha}
            />
          {:else}
            <PendingCommentCard
              comment={spec.comment}
              {currentHeadSha}
              ondelete={() => ondelete(spec.comment.id)}
            />
          {/if}
        {/each}
      {/if}
    </div>
  {/each}
</div>

<style>
  .comment-gutter {
    position: relative;
  }

  .comment-gutter__entry {
    position: absolute;
    left: 0;
    right: 0;
  }

  .comment-gutter__entry--hidden {
    visibility: hidden;
  }

  /* Cross-link highlight: the card whose source block is hovered (or vice
     versa) gets a neutral ring matching the block's tint. */
  .comment-gutter__entry--linked {
    outline: 2px solid color-mix(in srgb, var(--text-muted) 45%, transparent);
    outline-offset: 2px;
    border-radius: 6px;
  }

  /* Per-card "scroll to source" button — revealed on entry hover/focus. */
  .comment-gutter__jump {
    position: absolute;
    top: -8px;
    right: -2px;
    z-index: 3;
    display: flex;
    align-items: center;
    justify-content: center;
    width: 20px;
    height: 20px;
    padding: 0;
    font-size: 12px;
    line-height: 1;
    color: var(--text-muted);
    background: var(--bg-inset);
    border: 1px solid var(--diff-border);
    border-radius: 4px;
    cursor: pointer;
    opacity: 0;
    transition: opacity 0.1s ease;
  }
  .comment-gutter__entry:hover .comment-gutter__jump,
  .comment-gutter__jump:focus-visible {
    opacity: 1;
  }
  .comment-gutter__jump:hover {
    color: var(--text);
    border-color: var(--text-muted);
  }
</style>
