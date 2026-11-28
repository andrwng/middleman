# Dismiss the Claude review pill

## Why

After a Review with Claude run finishes, the "Claude · Done · N suggestions"
pill in the bottom-right dock stays on that PR for good. Closing the panel
folds it back into the pill, and the server keeps the latest run, so the pill
returns on every visit and reload until a new run replaces it. The reviewer
wants to clear it once they've read the summary.

## Goals

- A dismiss control (×) on the pill once the run has finished: done, failed,
  or stopped.
- Dismissing hides the pill for that run, across reloads and revisits.
- A new run shows the pill again.

## Non-goals

- Deleting the run or its suggestions. Suggestions in the diff are untouched.
- Reopening a dismissed run's summary. A new review can still be started from
  the toolbar.
- Dismissing a run that is still queued or running.

## Approaches

- **In memory only.** The pill would return on every reload, which is the
  complaint. Rejected.
- **On the server** (a column on `middleman_ai_review_runs` and a route).
  Survives across browsers, but needs a migration and an API change for a
  single-user app that runs in one browser. Rejected.
- **In `localStorage`, keyed by run.** Chosen; approved 2026-11-27.

## Design

- The run store (`packages/ui/src/stores/aiReviewRun.svelte.ts`) gains
  `isDismissed()` and `dismiss()`. `dismiss()` does nothing while the run is
  queued or running. Otherwise it records the run and closes the panel.
- Storage: `localStorage["claude-review-dismissed-runs"]`, a JSON array of
  `<id>@<created_at>` keys. `created_at` is part of the key so that a run id
  reused after a database reset isn't hidden by an old dismissal. Reads and
  writes go through try/catch, as in `collapseOverrides.ts`.
- `ClaudeReviewPanel.svelte` renders nothing for a dismissed run. For a
  finished run, the closed pill gets a separate × button with the label
  "Dismiss Claude review". The toggle and the × share one rounded wrapper,
  since a button can't contain a button.
- e2e: `cmd/e2e-server` seeds a finished run on `acme/widgets#7`, a PR no
  other e2e test opens. A new e2e-full spec dismisses the pill and reloads.

## Left to the builder

How the × is styled inside the pill, matching the panel's own close button.
