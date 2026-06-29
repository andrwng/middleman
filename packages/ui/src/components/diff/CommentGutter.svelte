<script lang="ts" module>
  import type { DraftComment } from "../../stores/diff.svelte.js";
  import type { PublishedReviewComment } from "../../stores/detail.svelte.js";
  import type { AIThread } from "../../stores/ai.svelte.js";

  export type CardSpec =
    | { kind: "draft"; key: string; comment: DraftComment }
    | { kind: "published"; key: string; comment: PublishedReviewComment }
    | { kind: "ai"; key: string; thread: AIThread };

  export type GutterEntry = {
    key: string;
    desiredTop: number;
    cards: CardSpec[];
  };
</script>

<script lang="ts">
  import { resolveStack } from "./gutterStack.js";
  import AIThreadCard from "./AIThreadCard.svelte";
  import ReviewCommentCard from "./ReviewCommentCard.svelte";
  import PendingCommentCard from "./PendingCommentCard.svelte";

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
