# PR focus mode: collapsible chrome around the diff

## Problem

When you're heads-down reviewing one PR, the surrounding chrome dominates the viewport. From top to bottom on the Review tab today:

1. Outer sidebar strip (repo + PR list) on the left.
2. Stack sidebar (when applicable).
3. Review nav (DiffSidebar) — commits, drafts, AI Q&A, file tree — always at a fixed width on the left.
4. Four stacked banners above the diff: `ReviewCoverBanner`, `CommitMessageBanner`, `PatchsetPicker`, `ReviewBriefCard`.
5. The diff itself, which gets whatever vertical space is left.

You can already collapse some of these (outer strip; cover banner; commit message), but unevenly: the patchset picker and the brief card have no persistent collapse, the review nav can't collapse at all, and the outer sidebar's resize handle is invisible at default width so the collapse path is undiscoverable. There's also no way to fold the four stacked banners into something compact without losing them entirely.

## Goal

Make the diff dominate the screen when the reviewer wants that, without removing access to any of the surrounding info.

Concretely:

- Every panel/section around the diff has a clearly-marked collapse, persisted across reloads.
- Collapsed states retain a thin, labeled affordance (a "rail" for side panels; a pill/pip in the consolidated strip) so the user knows what's tucked away.
- The four stacked banners gain a single "consolidate to one strip" action. The strip remembers each section's underlying collapsed state so toggling consolidation off restores what you had.
- The outer sidebar strip's resize/collapse handle becomes discoverable.

The mockup is committed alongside this spec at `docs/superpowers/specs/2026-05-23-pr-focus-mode-mockup.html` — left panel shows today, right panel shows the focused layout.

## Non-goals

- **No new "focus mode" master toggle.** The user curates per-section state; the consolidated strip is the only new aggregate control.
- **No per-PR scope.** All collapsed states are global (matches existing `pr-cover-collapsed`, `pr-commit-msg-collapsed` precedent). Per-PR variants are a future extension.
- **No stack management.** The orthogonal "this stack is stale; let me define one explicitly" gripe is parked for a separate spec.
- **No layout presets.** Single curated state per user, no named profiles.

## Per-surface design

### Outer sidebar strip — discoverability fix

`frontend/src/App.svelte` already persists `isSidebarCollapsed` (key `middleman-sidebar`) and a width (`middleman-sidebar-width`). The collapse-toggle button in `AppHeader.svelte` only appears when the sidebar is *already* collapsed (`isSidebarCollapsed() && isSidebarToggleEnabled() && !hasSidebarStrip`). So a user with an expanded sidebar at default width sees no collapse affordance — unless they happen to drag the resize handle, which is also poorly marked.

Fix: render an always-visible collapse chevron on the right edge of the expanded sidebar. When clicked, it collapses the sidebar (same code path as today's `toggleSidebar`). The existing resize handle stays; the chevron just makes the collapse direction obvious.

### Stack sidebar — new collapse-to-rail

`StackSidebar.svelte` is currently mounted from `PRListView.svelte`. It has no collapse today.

Add: a collapse button that shrinks the sidebar to a narrow vertical rail (~30px) showing a rotated "Stack: <name> · <n>" label. Click anywhere on the rail to expand back. Persist via `pr-stack-sidebar-collapsed` (global).

### Review nav (DiffSidebar) — resize + collapse-to-rail

`DiffSidebar.svelte` mounts (in order): `CommitListSection`, `PendingCommentsSection` (drafts), `QuestionsSection` (AI Q&A), the file tree. Today it's a fixed-width column.

Add:
- A resize handle on its right edge, matching the outer sidebar strip's behaviour. Persist `pr-review-nav-width` (global, default = current fixed width).
- A collapse-to-rail (~30px) showing rotated counts like `4c · 2d · 1q · 12f` (commits · drafts · questions · files). Click the rail to expand back. Persist `pr-review-nav-collapsed` (global).

### Top sections — fill the gaps, add consolidation

State today:

| Section | Collapse button? | Persists? |
|---|---|---|
| `ReviewCoverBanner` | Yes | `pr-cover-collapsed` |
| `CommitMessageBanner` | Yes | `pr-commit-msg-collapsed` |
| `PatchsetPicker` | No | — |
| `ReviewBriefCard` | Yes (chevron) | No (in-memory `$state(false)`) |

Changes:

- Add a collapse chevron + persistence to `PatchsetPicker` under key `pr-patchset-collapsed`.
- Add persistence to `ReviewBriefCard`'s existing chevron under key `pr-brief-collapsed` (mirroring how the cover banner saves state).
- Each section's collapsed state stays as today — a one-line header with the chevron, no body.

### Consolidated strip — new visual treatment

When the user clicks the new "consolidate" action, all four top sections fold into a single horizontal pill-strip immediately above the diff. The strip:

- Contains four "pips" (chips), one per section, labeled with the section name. The patchset pip shows the current patchset (e.g., `patchset 2/3`).
- Each pip's appearance reflects its section's underlying collapsed state: a fully-opaque pip means that section was previously expanded; a muted pip means it was already collapsed individually.
- Clicking a pip "peeks" the section: it expands inline immediately below the strip until clicked again or the user clicks another pip. The strip itself stays. **At most one section can be peeked at a time; peek state is ephemeral (in-memory, not persisted).**
- A leading chevron `‹` exits consolidated mode entirely; each section returns to its individual collapsed/expanded state from before consolidation. Underlying per-section states were never lost.

Persist consolidation state under `pr-top-sections-consolidated` (boolean, global).

Where the "Consolidate" affordance lives: a small chip pinned to the top-right of the four-banner container (introduced as a new wrapper around the existing four banners — `pr-top-sections` div in `PullDetail.svelte`). The chip is the only new chrome added when consolidation is OFF; everything else inside the wrapper renders as today. When consolidation is ON, the wrapper renders the strip instead of the four banners, and the chip is replaced by the strip's leading `‹` chevron.

## Persistence schema

All keys are `localStorage`, global (one user, one device), boolean unless noted. New keys introduced by this work:

| Key | Type | Default |
|---|---|---|
| `pr-stack-sidebar-collapsed` | boolean | `false` |
| `pr-review-nav-collapsed` | boolean | `false` |
| `pr-review-nav-width` | integer (px) | `260` (or whatever the current fixed width is) |
| `pr-patchset-collapsed` | boolean | `false` |
| `pr-brief-collapsed` | boolean | `false` |
| `pr-top-sections-consolidated` | boolean | `false` |

Existing keys are unchanged:

- `middleman-sidebar`, `middleman-sidebar-width` — outer sidebar.
- `pr-cover-collapsed` — `ReviewCoverBanner`.
- `pr-commit-msg-collapsed` — `CommitMessageBanner`.

## Components touched

- `frontend/src/App.svelte` — sidebar collapse-chevron affordance.
- `frontend/src/lib/stores/sidebar.svelte.ts` — no change unless the chevron click path needs a new reader (it already calls `toggleSidebar`).
- `packages/ui/src/components/detail/StackSidebar.svelte` — collapse-to-rail.
- `packages/ui/src/components/diff/DiffSidebar.svelte` — resize handle + collapse-to-rail.
- `packages/ui/src/components/diff/PatchsetPicker.svelte` — collapse chevron + persistence.
- `packages/ui/src/components/detail/ReviewBriefCard.svelte` — persist existing chevron.
- `packages/ui/src/components/detail/ReviewCoverBanner.svelte` — add the "Consolidate"/"Expand sections" action button.
- New file: `packages/ui/src/components/detail/TopSectionsStrip.svelte` — the consolidated pill-strip.
- `packages/ui/src/components/detail/PullDetail.svelte` — conditionally render either the four sections stacked or the strip + the peeked section, based on `pr-top-sections-consolidated`.

## Error handling

Persistence reads/writes are wrapped in `try/catch` per existing convention (matches `safeGetItem` / `safeSetItem` in the diff store). A blocked or full localStorage degrades to in-memory only — no functional regression.

## Testing

Component-level vitest:

- `StackSidebar.test.ts` (new or extended): click the rail expands; click the chevron collapses; persistence key is set.
- `DiffSidebar.test.ts` (extended): resize handle drags update `pr-review-nav-width`; collapse button toggles `pr-review-nav-collapsed`; the rail shows the four section counts.
- `PatchsetPicker.test.ts` (new): chevron toggles `pr-patchset-collapsed`.
- `ReviewBriefCard.test.ts` (new): expanded state persists across re-mount via `pr-brief-collapsed`.
- `TopSectionsStrip.test.ts` (new): pip click expands one section inline; second click collapses it; `‹` chevron clears `pr-top-sections-consolidated`; underlying section states are preserved across the round-trip.
- `PullDetail.test.ts` (extended) or a dedicated layout test: with `pr-top-sections-consolidated=true` only the strip + diff render; toggling renders the four banners.

Manual smoke (one pass): walk every collapse, reload, confirm state survives; toggle consolidation with mixed underlying collapse states and confirm round-trip; resize the review nav and reload.

## Future extension (not implemented)

- Per-PR persistence (override the global default for specific PRs).
- Named layout presets ("review mode", "skim mode", "debug mode") selected from a menu.
- A single "focus mode" master toggle that snaps everything into a maximum-collapsed preset in one click. The mechanics here support adding that later without redesign — it would just write all six new keys at once.
- Explicit stack management (the orthogonal gripe that surfaced during brainstorming).
