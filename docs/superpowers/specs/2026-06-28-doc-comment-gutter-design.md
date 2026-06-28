# Doc Comment Gutter — Design

Date: 2026-06-28
Status: Approved (design); spec under review
Builds on: 2026-06-26-worktree-markdown-doc-review-design.md (the doc viewer)

## Problem

In the rendered worktree-doc viewer (`DocReviewSurface` → `RenderedMarkdownView`),
comment / Ask-Claude cards render **inline** — injected into the prose flow
after the anchored block by an imperative `$effect` that, on every change,
tears down *all* `.rmd-thread-wrap` nodes and re-injects them. Problems:

- **Reading clutter:** cards interrupt the prose, and the doc body is narrow so
  cards land mid-page.
- **Churn (the likely "disappears after save"):** a real-browser repro showed
  the card *does* inject and is visible — there is **no reactivity bug**. But
  the tear-down-and-re-inject-all pass changes document height on every edit; in
  a long doc that can jump the scroll so the just-added card (and the reader's
  place) shift out of view. The tiny repro doc couldn't trigger it; a declarative
  gutter avoids the mechanism entirely.
- **Wasted space:** with the left nav collapsed the rendered doc uses only ~half
  the width; the right half is empty.

## Goal

A **comment gutter**: an opt-in display mode for the rendered doc view where
comment/AI cards render **declaratively** in a right-hand margin, each aligned to
its anchor block's vertical position, with push-down so cards never overlap.
Commented blocks get a subtle gutter-edge marker; the composer opens in the
gutter too.

## Prerequisite fix: boundary double-injection (shared bug)

Independent of the gutter, the repro found that commenting on a block at a block
boundary (e.g. a heading) renders the card **twice**. Root cause: marked v17
includes the trailing blank line in a token's `raw`, so the block end-line at
`RenderedMarkdownView.svelte:386` (`currentBlockStart + countNewlines(rawText)`)
is inflated — the block's half-open range extends into the following blank line
and abuts/overlaps the next block, so `anchorOverlapsBlock`'s boundary match
(`anchorEnd >= blockStart`) matches both blocks.

Fix the **range math**, not the overlap operator: compute a block's end-line
from its *content* lines (trim the trailing blank-line run, floor at one line) so
blocks don't overlap at boundaries. Keep the start-line cursor walk
(`:305`, `cursorLine += countNewlines(rawText)`) using the full count — it must
account for blank-line separators to land the next block's start correctly.
Do **not** change `anchorOverlapsBlock` `>=` to `>` (that breaks a legitimate
single-line block whose anchor sits on its own start line). Cover with a unit
test across shapes: heading+paragraph, adjacent blocks, single-line, multi-line,
last block — each renders exactly one card on the correct block. This is shared
by inline and the gutter (both use `cardsForRange`), so it lands first.

## Non-goals (v1)

- Interactive block↔card linking (hover to lift, click-to-scroll, connectors).
- The gutter in the diff's "rendered markdown" toggle — that stays inline.
- Any change to the comment data model, anchoring, or the card components.
- Full Google-Docs text highlighting of the commented span (a block-edge marker
  is enough).

## Decisions (confirmed)

1. **Scope:** doc view only. Gutter is an opt-in mode; the diff's rendered
   toggle and narrow viewports keep the existing inline path.
2. **Composer:** opens in the gutter, aligned to its block.
3. **Collisions:** push-down — a card that would overlap the one above is nudged
   down; cards never overlap.
4. **Marker:** a gutter-edge accent bar + small comment glyph on commented
   blocks (no prose recolor).

## Architecture

### Mode + layout
- `RenderedMarkdownView` gains `commentLayout: "inline" | "gutter"` (default
  `"inline"`). `DocReviewSurface` passes `"gutter"`.
- Gutter mode makes `.rmd-view` two columns: doc body left (~80ch max-width) and
  a `position: relative` gutter right.
- **Narrow-viewport fallback:** below the review layout's existing breakpoint
  (`max-width: 720px`), gutter mode renders via the existing inline path. (Note:
  the inline churn above is also why a future inline cleanup may be worthwhile,
  but it's out of scope here.)

### Pure positioning core (testable without DOM)
- `resolveStack(items: { desiredTop: number; height: number }[], gap: number): number[]`
  — sort by `desiredTop`; place each at `max(desiredTop, prevBottom + gap)`;
  return tops in input order. DOM-free, in its own module
  (`packages/ui/src/components/diff/gutterStack.ts`), unit-tested directly.

### CommentGutter subcomponent
- `packages/ui/src/components/diff/CommentGutter.svelte`.
- Renders the entries **declaratively** (`{#each}` — NOT the imperative `mount()`
  the inline path uses; that's the whole point — Svelte manages the DOM, so no
  tear-down-and-re-inject churn).
- Reuses the existing card components (`AIThreadCard` / `ReviewCommentCard` /
  `PendingCommentCard`) and the composer (`DiffComposer` / `AIAskComposer`).
- Positioning: each entry is absolutely positioned at its resolved `top`. Cards
  must be rendered **already positioned on first paint** (no flash): measure
  heights and apply `resolveStack` before/synchronously with the first
  positioned render — the same lesson as the composer scroll-flash fix
  (`e8454cf`); otherwise we reintroduce a jump. Re-runs when the entry set or
  measured heights change.

### Block offsets (robust measurement)
- Aligning a card to its block needs the block's vertical offset within the
  gutter's positioning context. The existing `computeBlockBottom`
  (`bodyEl.offsetTop + target.offsetTop + …`) **double-counts** `bodyEl.offsetTop`
  because `.rmd-body` is statically positioned (so `target.offsetTop` is already
  relative to `.rmd-view`) — this is why the composer lands "about" right, ~16px
  low. Switch to a bounding-rect delta
  (`target.getBoundingClientRect().top - container.getBoundingClientRect().top + container.scrollTop`)
  or an equivalently robust measure; the composer positioning benefits too.

### RenderedMarkdownView in gutter mode
- The block walk keeps adding the per-block hover `+`/`?` affordance, but in
  gutter mode does NOT inject inline `.rmd-thread-wrap`. Instead it builds the
  gutter entry list: for each block with a non-empty `cardsForRange`, compute the
  block's `desiredTop` and collect its cards; the open composer becomes an entry
  at its block's `desiredTop`.
- Commented blocks get a `.rmd-block--commented` class → CSS gutter-edge accent
  bar + small comment glyph (we already know which blocks have cards).
- Inline mode unchanged (diff view + narrow fallback).

## Data flow
1. `DocReviewSurface` renders `RenderedMarkdownView … commentLayout="gutter"`.
2. RenderedMarkdownView renders the body, walks blocks, builds gutter entries
   `{ blockTop, cards }` (+ composer entry) from `cardsForRange` + block offsets,
   marks commented blocks.
3. `CommentGutter` renders entries (`{#each}`), measures heights, runs
   `resolveStack`, positions them. A newly-saved draft / AI thread updates
   `cardsForRange` → a new entry appears at its block, with no doc-flow churn.

## Reuse vs change
- Reused: card components, `cardsForRange`, the inline path (diff view + narrow
  fallback).
- New: the boundary-math fix (+ test); `resolveStack` (pure); `CommentGutter`
  (declarative); the `commentLayout` prop; the gutter branch of the block walk;
  robust block-offset measurement; the `.rmd-block--commented` marker.

## Testing
- **Unit:** boundary-math fix (one card per boundary block across shapes);
  `resolveStack` (no overlap, push-down, order preserved, gap, empty).
- **Component (vitest):** gutter mode renders cards in the gutter container (not
  inline `.rmd-thread-wrap`); a commented block carries `.rmd-block--commented`;
  a post-mount `addDraftComment` adds a gutter entry. (Structural — jsdom has no
  layout; pixel `top`s are covered by `resolveStack`'s unit tests.)
- **Real-browser (Playwright):** in the doc view, add a comment → exactly one
  card appears in the gutter (guards both the boundary bug and the
  show-after-save path the unit layer can't fully exercise).
- **Unaffected:** existing inline `RenderedMarkdownView` + `DocReviewSurface`
  tests (default mode) stay green.
- **Manual/visual:** placement, push-down spacing, marker styling, narrow fallback.

## Open questions / future
- Interactive block↔card linking (hover/scroll/connectors) — deferred.
- An inline-path cleanup to stop the tear-down-and-re-inject churn (would help the
  diff's rendered toggle + the narrow fallback) — deferred, separate.
