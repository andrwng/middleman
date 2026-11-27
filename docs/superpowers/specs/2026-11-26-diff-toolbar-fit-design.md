# Diff toolbar fits narrow windows

## Intent

In the PR diff view, the toolbar above the file diffs is a single row that
never wraps. It contains Layout, Tab width, Hide whitespace, Refresh, Refs,
Browse, the GitHub link, Review with Claude, and Review. It sits inside
`.diff-main`, which has `overflow: hidden`
(`packages/ui/src/components/diff/DiffView.svelte:766`), so in a narrow
window the controls on the right are clipped with no scrollbar and cannot
be reached. The goal is that every toolbar control is reachable at any
window width, and that the toolbar stays one row at ordinary laptop widths.

Widths below are estimates from the CSS. With nothing extra showing, the
toolbar needs about 1000px. It needs about 1300px when the GitHub link,
the hidden-thread toggle, pending comments, and a Claude run are all
showing. With the default sidebars (340px PR list and 280px review nav),
a 1440px window gives the diff column about 820px.

## Goals

- No toolbar control is clipped or unreachable, at any width.
- One row in a 1440px window with the default sidebars, including when
  everything is showing.
- Spacing stays as it is today. The user does not want the gaps
  between groups squeezed.

## Non-goals

- Changing what any control does. No store, API, or persistence changes.
- Changing the Claude entry (the Review with Claude button, the run pill
  and Stop), the Review button, or the GitHub link.
- Moving the "Show hidden N" toggle. It only appears when threads are
  hidden, and its count is the cue that they exist.

## Anti-goals

- Horizontal scrolling. See Approaches.
- Putting the hide-whitespace setting where nobody can see it. The
  setting persists across sessions and silently removes changes from the
  diff.

## Approaches

- **A. Icon-only buttons plus tighter spacing (about 200px saved).**
  Refresh, Refs, and Browse drop their text, the gaps between groups
  shrink from 20px to 12px, and "Tab width" becomes "Tab". On its own
  this still wraps at 1440px when everything is showing.
- **B. A's icon-only buttons plus a View dropdown (about 500px saved).
  Chosen.** Layout, Tab width, and Hide whitespace move behind one View
  button. This fits at 1440px even when everything is showing. The cost
  is that switching between unified and split takes two clicks. The
  user picked B without A's tighter spacing, which B does not need.
- **C. Size-dependent labels.** Full labels when there is room, icons
  only when the toolbar is narrow, driven by a container query. Nothing
  changes on a large monitor, but there are two looks to keep working.
- **Scrolling, ruled out.** Setting `overflow-x: auto` on the toolbar
  forces `overflow-y` to `auto` as well. That clips the Review with
  Claude popover, which is absolutely positioned and hangs below the
  toolbar. Off-screen controls would also stay invisible until scrolled
  to, which is today's problem.

## Design

1. **View dropdown.** One "View" button replaces the three left-hand
   groups at the left end of the toolbar. Clicking it opens a popover
   anchored under the button and aligned to its left edge. The popover
   holds today's controls, with their full labels and unchanged
   behavior: the Layout segmented control (Unified / Split), the Tab
   width segmented control (1 / 2 / 4 / 8), and the Hide whitespace
   switch. A change applies immediately and the popover stays open, so
   several settings can be changed in one visit. The popover closes on
   a click outside it, on Escape, and on a second click of View.
   *Claude's call:* the popover reuses the existing segmented controls
   rather than the shared `FilterDropdown`. A segmented control is more
   compact than a seven-row checklist, and `FilterDropdown` hard-codes a
   funnel icon, which would read as "filter".
2. **Whitespace cue.** While Hide whitespace is on, the View button
   shows a small marker, and its tooltip says that whitespace changes
   are hidden. Layout and tab width get no marker because both are
   visible in the diff itself. *Claude's call.*
3. **Icon-only Refresh, Refs, and Browse.** Each keeps its existing SVG
   icon and its existing `title` tooltip, which already names the
   action and the shortcut. Each gains an `aria-label` ("Refresh",
   "Refs", "Browse"), so screen readers and the existing tests that find
   these buttons by accessible name or by title keep working. The
   Refresh spinner is unchanged.
4. **Wrap as a last resort.** The toolbar row gets `flex-wrap: wrap`
   with a small row gap. Anything that still does not fit moves to a
   second line instead of being clipped. The right-hand group stays
   right-aligned on whichever line it lands on.
5. **Spacing.** The 20px gap between groups is unchanged.

## Testing

- **Unit tests** (`packages/ui/src/components/diff/DiffToolbar.test.ts`):
  - View opens and closes on its button, on an outside click, and on
    Escape.
  - The controls inside View call `setLayout`, `setTabWidth`, and
    `setHideWhitespace`.
  - The whitespace marker shows only while Hide whitespace is on.
  - The icon-only buttons are still found by accessible name.
- **E2E reachability test.** At narrow viewports (1024px and 1280px,
  default sidebars), assert that every toolbar control's bounding box
  lies inside the visible diff column. The test compares bounding boxes
  rather than relying on `click()` or `hover()`, because Playwright
  scrolls elements into view programmatically, even inside
  `overflow: hidden`, and so reaches controls a person cannot. The test
  must fail against today's code before the fix.
- **Existing e2e updates.** The tests in
  `frontend/tests/e2e-full/diff-view.spec.ts` that drive tab width
  (line 352) or the whitespace switch (lines 391, 825, 984) open View
  first. Refs and Browse are found by title in the e2e suite, and their
  titles do not change.

## Left to the builder

- The View button's icon and the marker's exact look.
- Popover z-index and stacking. The Review with Claude popover in the
  same file is the reference.
- Whether opening View closes an open Review with Claude form.
- Whether the right-hand group also wraps internally at very narrow
  widths.
