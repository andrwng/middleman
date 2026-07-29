# Delete a Hidden Thread from Its Collapsed Stub — Design

Date: 2026-07-29
Status: Approved (design)
Builds on: the local review-threads UI (`ReviewThreadCard.svelte`), and its
just-added hidden-stub status cue (`2026-07-29-hidden-thread-status-cue-design.md`).

## Problem

Deleting a hidden review thread requires re-expanding it first: `Show` → then the
expanded card's `Delete`. Once a comment has been addressed, the hidden thread is
often just clutter the reviewer wants gone — the re-expand step is friction on a
routine cleanup.

## Goal

Let the reviewer delete a hidden thread directly from its collapsed stub, without
un-hiding it — using the same safe, permanent delete the expanded card already offers.

## Decision (user-approved)

Add a `Delete` affordance to the collapsed stub, reusing the existing **two-click
confirm** flow (`Delete` → `Confirm?` → deletes). Deletion is permanent (removes the
thread and its comments, no undo), so the confirm step is kept — matching the expanded
card and guarding against an accidental stray click.

## What already exists (reused, not rebuilt)

- **The hidden stub** — `ReviewThreadCard.svelte`, the `{#if thread.hidden}` branch:
  a `.review-thread--hidden` flex row (`gap:8px`) with a `flex:1` label, the status
  pill, and the `.review-thread__unhide` ("Show") button.
- **The delete flow** — `confirmingDelete` state + `onDelete()` (first click sets
  `confirmingDelete = true`; second click clears it and calls
  `reviewThreads.deleteThread(thread.id)`), and the expanded card's
  `.review-thread__action--delete` button styling (red on hover). All already present
  for the expanded card.

## Design

- In the `{#if thread.hidden}` branch, add a `Delete` button after `Show`, wired to the
  existing `onDelete`, with its label driven by the existing state:
  `{confirmingDelete ? "Confirm?" : "Delete"}`. It reuses `.review-thread__action--delete`
  (or the stub's muted button look with the red-on-hover delete accent) so it reads:

  ```
  Hidden thread   (resolved)   Show   Delete
  (click Delete)
  Hidden thread   (resolved)   Show   Confirm?
  (click again -> thread deleted)
  ```

- **State reuse is safe:** the stub (`{#if thread.hidden}`) and the expanded card
  (`{:else}`) never render at the same time, so sharing `confirmingDelete`/`onDelete`
  across both is clean — only one Delete button exists at any moment.
- **Reset the pending confirm on Show:** the stub's `Show` (unhide) handler also clears
  `confirmingDelete`, so an abandoned confirm on the stub doesn't carry over and make
  the expanded card's `Delete` read `Confirm?` after un-hiding.
- No change to delete semantics (still `deleteThread` — permanent thread + comments
  removal); no server/store/API change.
- Applies to both `variant="inline"` and `variant="gutter"` — the stub is shared.

## Non-goals

- No one-click delete (confirm is deliberate) and no undo.
- No new/bulk delete semantics; `deleteThread` is unchanged.
- No server/store/DB/API change.
- No change to Show/unhide, resolve, hide, or the status cue beyond the confirm reset
  noted above.

## Edge cases

- Click `Delete` then `Confirm?` → thread deleted (the card unmounts as the thread
  leaves the list).
- Click `Delete` then `Show` → unhide proceeds and the pending confirm is cleared (see
  "Reset the pending confirm on Show"), so the expanded card opens with a plain
  `Delete`.

## Testing

- **vitest (`ReviewThreadCard.test.ts`):** on a hidden thread, a first `Delete` click
  shows `Confirm?` and does NOT call `deleteThread`; a second click calls
  `deleteThread(thread.id)`. The `Show` button still unhides, and (regression) the
  status pill still renders. Assert against the rendered stub, not mocks.
- Pure render/interaction change with no API/cross-layer dimension → component test is
  the right level; no new Playwright e2e.
- **Green bars:** `make frontend-check` 0/0, vitest.
