# Hidden-Thread Status Cue — Design

Date: 2026-07-29
Status: Approved (design)
Builds on: the local review-threads UI (`ReviewThreadCard.svelte`), rendered in both
the diff view (`variant="inline"`) and the worktree doc gutter (`variant="gutter"`).

## Problem

When a review thread is hidden, `ReviewThreadCard` collapses to a stub — `Hidden
thread [Show]` — that ignores `thread.status`. A reader can't tell a hidden thread
that was resolved (dealt with, put away) from one that is still open (hidden to
declutter, but unaddressed) without un-hiding it.

## Goal

Show the thread's status on the collapsed hidden stub, so resolved vs. still-open is
visible at a glance without un-hiding.

## Decision (user-approved)

Reuse the existing `.review-thread__status` pill — the same one the expanded card
header renders — on the hidden stub, showing the full status word
(`resolved` / `open` / `discussed` / `applied`). This distinguishes resolved from the
rest (a superset of the resolved-vs-not ask) at no extra cost and keeps the vocabulary
consistent with the expanded card.

## What already exists (reused, not rebuilt)

- **The hidden stub** — `ReviewThreadCard.svelte`, the `{#if thread.hidden}` branch:
  a `.review-thread--hidden` row with a `flex:1` `.review-thread__hidden-label`
  ("Hidden thread") and a `.review-thread__unhide` ("Show") button.
- **The status pill** — the `.review-thread__status` span + its CSS, already rendered
  in the expanded (`{:else}`) header from `{thread.status}`.
- **The data** — `thread.status` (`open|discussed|applied|resolved`) is already on the
  `ReviewThread` type; no fetch or store change is needed.

## Design

In the `{#if thread.hidden}` branch, render a `.review-thread__status` pill showing
`{thread.status}`, seated in the stub's flex row between the `flex:1` label and the
`Show` button, so it reads:

```
Hidden thread            (resolved)   Show
Hidden thread            (open)       Show
```

- Reuse the existing `.review-thread__status` styling; add only whatever minimal
  spacing is needed to seat the pill in the stub row (e.g. a small right margin).
- Applies to both `variant="inline"` and `variant="gutter"` — the hidden stub is
  shared, so both surfaces get the cue.
- Purely presentational: no DB, server, store, or API change; `Show`/unhide behavior
  is unchanged.

## Non-goals

- No new statuses, no status editing from the stub, no filter/sort by status.
- No change to when/whether a thread is hidden, or to the unhide flow.
- No server/DB/store change (the status is already present client-side).
- No treatment beyond the status pill (no extra color-coding of the stub) unless a
  follow-up asks for it.

## Edge cases

- Every thread has a `status`, so the pill always renders in the stub (there is no
  "no status" case).
- A hidden thread can carry any status (hidden is orthogonal to status); the pill
  reflects whatever it is.

## Testing

- **vitest (`ReviewThreadCard.test.ts`):** a hidden + `resolved` thread renders the
  stub with `resolved` shown; a hidden + `open` thread shows `open`; the `Show` button
  is still present and unhides. Assert against the rendered stub, not mocks.
- Pure render change with no API/cross-layer dimension, so a component test is the
  right level — no new Playwright e2e.
- **Green bars:** `make frontend-check` 0/0, vitest.
