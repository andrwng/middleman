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
    ondelete: (id: string) => void;
  }

  const { entries, repoOwner, repoName, currentHeadSha, ondelete }: Props = $props();

  // Resolved top offsets, one per entry (index-aligned). The effect sets these
  // before visibility is revealed, so wrappers are never painted at incorrect
  // positions. Start empty; the effect fills them on first run.
  let resolvedTops = $state<number[]>([]);

  // Bound wrapper elements, index-aligned with entries.
  let wrapperEls = $state<(HTMLElement | undefined)[]>([]);

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
</script>

<div class="comment-gutter">
  {#each entries as e, i (e.key)}
    <div
      class="comment-gutter__entry"
      class:comment-gutter__entry--hidden={!visible}
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
</style>
