# Refs search without selection, an `s` hotkey, and a jump-back stack

## Context

The symbol references gutter answers "where else does this appear?" but only from a
selection: you must scroll to an occurrence of the symbol, highlight it, and click Refs on
the floating toolbar. That is fine when you are already looking at the symbol and useless
when you are not — which is most of the time you want the answer.

This adds three things, in this order:

1. A **Refs button in the diff toolbar** that opens the refs gutter with a search box, so a
   lookup needs no selection and no scrolling.
2. An **`s` hotkey** for the same thing.
3. A **Back button inside the refs window** that walks you home through the positions you
   jumped from, so clicking through a result list is no longer a one-way trip.

Everything downstream of the query — the endpoint, the grep, ctags classification, grouping,
kind sort, the comment/string expander, the repo-wide count, click-to-jump, the
"not in this view" marker — is reused unchanged. This spec adds entry points and one piece
of state; it does not change what a search returns.

Scope is the code diff surface (`DiffView` -> `DiffToolbar` / `SymbolRefsGutter`), which
serves both real GitHub PRs and local worktrees through the same component tree.

## Decisions (locked with the user)

| Question | Decision |
|---|---|
| What the Refs button expands into | An **input in the refs gutter header**, where the query text renders today. Search UI and results are one surface. Not a popup palette, not an inline toolbar field. |
| What Back undoes | **The position you jumped from.** A jump pushes where you were leaving; Back pops and returns there. Gutter contents never change on Back. |
| When positions are pushed | **On jump only**, and only once the jump has actually moved the reader — a jump that resolves `"missing"` pushes nothing. Not on search. |
| What counts as "the position you jumped from" | The most **deliberate** position available, in this order: the symbol a search was launched from, else the line the last jump landed on, else the line under the viewport's midline. A scroll offset is not a place a person can name, so it is only ever the fallback. |
| Bidirectional? | **No.** Back only pops. There is no Forward. |
| Stack depth | Modest and fixed: `MAX_BACK_DEPTH = 10`, oldest dropped. |
| Hotkey | `s`, unmodified. |
| Esc in the search input | Closes the refs gutter, matching the existing `x` button. |

Consequence worth stating up front: because Back never pushes what it leaves, the stack
strictly drains. There is no state in which Back is available forever.

## Facts this design rests on

- **`flashDiffLine` scrolls with `block: "center"`.** A jump therefore leaves its target at the
  *middle* of the viewport, and it adds its highlight class *synchronously* while the scroll is
  still travelling. This design originally missed both halves of that, and each cost a bug — see
  Amendments below. Anything that records "where the reader is" has to agree with this function
  about what a jump does.

Each verified against the current tree, not recalled.

- `isActive()` is exactly `status !== "idle"` (`stores/symbolRefs.svelte.ts:85-87`), and the
  gutter mounts on it (`DiffView.svelte:338`). A new non-idle status therefore opens a blank
  gutter with **no change to the mount condition**.
- `DiffView.svelte:201` already installs a `window` keydown listener. Its guards skip
  `INPUT`/`TEXTAREA`/`SELECT`, `isContentEditable`, and any `metaKey`/`ctrlKey`/`altKey`
  press, then dispatch unmodified single letters (`j`, `k`, `[`, `]`, `m`). The input guard
  is what makes typing `s` in the new search box safe for free.
- `s` is unbound. Surveying every `key === "..."` comparison in `packages/ui/src` yields
  `Enter`, `Escape`, space, arrows, `Tab`, `n`, `j`, `p`, `m`, `k`, `[`, `]`, `?`.
- `DiffView.svelte:115` already holds `currentSha = $derived(diffStore.getCurrentCommitSha())`.
  That function can legitimately return `""` — an unresolved patchset pair, or no commits
  (`stores/diff.svelte.ts:797`). The existing selection-side Refs button already gates on
  it being non-empty, because line numbers found against no SHA cannot be resolved.
- `DiffView.svelte:272-273` already calls `symbolRefsStore.close()` when the diff scope
  changes or the head SHA drifts. Anything cleared by `close()` inherits that staleness
  rule rather than reimplementing it.
- `.diff-area` is the scroll container (`DiffView.svelte:324`, `overflow: auto` at `:428`),
  bound as `diffArea`.
- `findDiffLineEl` (`components/diff/scrollToDiffLine.ts`) resolves a position by querying
  `document` for `.diff-file[data-file-path="P"] [data-anchor-line="N"][data-anchor-side="RIGHT"]`.
  It deliberately does not require `.line-wrap`, so it also matches lines revealed out of a
  collapsed region. This is the precedent for querying from `document` instead of threading
  a container prop through components.
- `isSymbolQuery(text)` is already exported from the store module and rejects empty input,
  **any** whitespace, and anything over `MAX_QUERY_BYTES`.
- `SymbolRefsGutter`'s props are `{ owner, name, number, width }`, and it already reaches
  `diffStore` inside `jumpTo`. It can therefore read the SHA and submit a search itself
  without new props.
- jsdom reports `getBoundingClientRect()` as all zeros and does not implement
  `elementFromPoint`. Layout-reading code cannot be tested in place; `SymbolRefsGutter.test.ts`
  already handles this by stubbing the whole jump via `scrollToDiffLineMock`.
- Every test file this touches already exists: `stores/symbolRefs.svelte.test.ts`,
  `components/diff/SymbolRefsGutter.test.ts`, `DiffToolbar.test.ts`, `DiffView.test.ts`,
  `scrollToDiffLine.test.ts`, and `frontend/tests/e2e-full/symbol-refs.spec.ts` (417 lines,
  5 tests). No new test file is needed.
- `ShortcutHelpModal` is roborev-only (`views/ReviewsView.svelte:261`). The diff view's
  shortcuts are documented nowhere, so there is no help surface for `s` to join.
- No endpoint, response field, or schema changes. **`make api-generate` is not needed** and
  the diff is frontend-only.

## Architecture

The `symbolRefs` store gains the new state; the gutter gains the UI; `DiffToolbar` and
`DiffView` gain entry points. One new function joins `scrollToDiffLine.ts`.

**The back stack lives in the `symbolRefs` store, not the diff store.** It is this feature's
trail, its UI is in the refs window, and keeping it out of the diff store avoids inventing a
general navigation contract that the five other jump-to-line features would arguably have a
claim on. The payoff is the staleness property above: the stack clears in `close()`, and
`DiffView` already calls `close()` on scope change and SHA drift, so a stored position cannot
outlive the hits it was captured alongside. Line-number provenance has been this feature's
recurring failure mode; this arranges for the new state to be governed by the existing rule
rather than a parallel one.

| File | Change |
|---|---|
| `packages/ui/src/stores/symbolRefs.svelte.ts` | `"prompt"` status; `openBlank()`; `focusSeq`; `pushPosition` / `popPosition` / `canGoBack` / `MAX_BACK_DEPTH`; the search `origin` (`setOrigin` / `getOrigin` / `clearOrigin`); `close()` clears the stack and the origin |
| `packages/ui/src/components/diff/SymbolRefsGutter.svelte` | header query text becomes an input; prompt state; Back button; push-before-jump in `jumpTo` |
| `packages/ui/src/components/diff/DiffToolbar.svelte` | Refs button beside refresh |
| `packages/ui/src/components/diff/DiffView.svelte` | `s` in `handleKeydown`; wire the toolbar button |
| `packages/ui/src/components/diff/scrollToDiffLine.ts` | `currentDiffPosition()`, `highlightedDiffPosition()` |
| `packages/ui/src/components/diff/DiffFile.svelte` | `computeSymbolSelection()` reports the selection's line and side, not just its text; the selection-side Refs button records the search's origin |

## Piece 1: search without selecting

`SymbolRefsStatus` gains `"prompt"` beside `idle | loading | ready | error`. `openBlank()`
sets it **only when the store is idle**, and always increments `focusSeq`. The gutter runs an
`$effect` on `focusSeq` that focuses and selects the input.

That single method covers both situations. Opening fresh gives an empty prompt. Reaching for
the search box while results are showing leaves the results completely alone and only
refocuses — select-all means typing replaces the query anyway, and discarding a result list
because the user reached for the input would be a bad trade.

The header today renders `getQuery()` in a span beside the count and the close button. It
becomes an input bound to a local `draft`, seeded from `getQuery()`. The input is present in
**every** status, not only `"prompt"` — it replaces the span outright, so a query can always
be edited and re-run in place, including from the error state. What `"prompt"` controls is
the gutter **body**: a short hint instead of a result list.

```
prompt state                          after Enter
+------------------------+            +------------------------+
| [find a symbol...]   x |            | [handle          ] 7 x |
+------------------------+            +------------------------+
| Type a symbol name and |            | src/v/kafka/handler.cc |
| press Enter.           |            |  12 DEF  kafka::handle |
+------------------------+            |  40 DEF  handle(req&&) |
```

Submit on Enter validates with `isSymbolQuery(draft)` and, on failure, shows the reason
inline instead of firing. The whitespace rule makes this load-bearing rather than decorative:
typing `group manager` is rejected, and a search box that silently does nothing would be
baffling. On success it calls
`search(owner, name, number, diffStore.getCurrentCommitSha(), draft)`.

Esc closes the gutter, matching the existing close button.

The header now carries four controls, in this order, so the input keeps the flexible width it
has today and the two buttons stay at fixed ends:

```
+---------------------------------------------+
| [<]  [ handle                    ]  7   [x] |
+---------------------------------------------+
  Back    input (flex: 1)          count  close
```

Back is disabled rather than hidden when the stack is empty, so the input does not shift
position as the trail grows and drains.

The **toolbar button** sits beside refresh in `DiffToolbar`, disabled (not hidden) with an
explanatory tooltip when `getCurrentCommitSha()` is `""`, and its tooltip names the `s` key
since no shortcut help surface exists. The selection-side Refs button in `DiffFile` is
untouched, and both paths call the same `openBlank()`.

## Piece 2: the `s` hotkey

Added to `DiffView`'s existing `handleKeydown`, whose guards already do the work:

```ts
if (e.key === "s") {
  if (currentSha === "") return;
  e.preventDefault();
  symbolRefsStore.openBlank();
}
```

Like the existing `j`, `k`, `m`, `[`, `]`, this fires even when a modal is open over the
diff. That matches the established behaviour of this handler; making one key modal-aware
where the others are not would be the surprising choice.

## Piece 3: the jump-back stack

Store: a `DiffPosition` is `{ path: string; line: number }`.

- `pushPosition(p)` appends, dropping the oldest beyond `MAX_BACK_DEPTH = 10`.
- `popPosition()` returns the most recent or `null`.
- `canGoBack()` reports whether the stack is non-empty.
- `close()` clears it. `search()` does **not** — the trail survives a new search, since the
  positions remain valid for the same SHA.

Gutter: `jumpTo(hit)` captures `currentDiffPosition()` and pushes it before jumping. A Back
button in the header pops and feeds the result to the **same** `scrollToDiffLine` call with
the **same** deps object used by row clicks, so Back inherits expand-the-collapsed-region,
scroll, and the jump highlight without new code. Back never pushes what it leaves.

```
reading handler.cc:40
search "handle"              stack []
click group.cc:12            stack [handler:40]
click metadata.cc:88         stack [handler:40, group:12]
Back -> group.cc:12          stack [handler:40]
Back -> handler.cc:40        stack []
Back -> disabled
```

Two guards. Clicking the row you are already on does not push a duplicate. If a popped
position no longer resolves — its file collapsed away, its region gone — it has already left
the stack, so pressing Back again tries the next entry, with a short note in the header
rather than a silent nothing.

`currentDiffPosition()` queries `.diff-area` for anchored lines and returns the first one at
or below the container's top edge, or `null` when there are none. It queries `document`
rather than taking a container prop, following `findDiffLineEl`. Because jsdom has no layout,
it lives in `scrollToDiffLine.ts` as its own exported function so gutter tests can mock it
the way they already mock the jump, and it carries its own unit test over a fixture with
stubbed rects.

## Testing

Behaviour is pinned wherever it is cheapest to pin.

- **Store** — `openBlank()` from idle vs. with results; the depth cap dropping the oldest;
  LIFO pop and `canGoBack()` transitions; `close()` clearing the stack; and the one most
  likely to regress, `search()` **not** clearing it.
- **Gutter** — Enter fires with the store's SHA; empty and whitespace queries do not fire and
  state why; Esc closes; `focusSeq` refocuses; Back's disabled/enabled transitions;
  push-on-jump; the no-duplicate-push guard; the unresolvable-pop note. `currentDiffPosition`
  is mocked here.
- **`currentDiffPosition`** — its own unit test with stubbed rects, including the empty case.
- **`DiffToolbar`** — button present; disabled on empty SHA; click calls `openBlank()`.
- **`DiffView`** — `s` fires; ignored while an input is focused; ignored with a modifier;
  ignored on empty SHA.
- **e2e**, two additions to the existing spec, placed so each piece is proven end-to-end
  before the next begins rather than all at the end: the button and `s` both open a focused
  input and a typed query returns rows through the real server; then clicking two rows and
  pressing Back walks home.

## Tasks

1. Store: `"prompt"` status, `openBlank()`, `focusSeq`.
2. Gutter: header input, prompt state, validation, Esc.
3. Toolbar Refs button + `DiffView` wiring.
4. Hotkey `s`.
5. e2e: search without selecting (covers 1-4).
6. Store: back stack.
7. `currentDiffPosition()` helper.
8. Gutter: push-on-jump + Back button.
9. e2e: Back walks the trail home.

## Verification

- `cd frontend && bun run test` (vitest covers `packages/ui` via the vite config's include)
- `make frontend-check` from the repo root
- Test-inclusive typecheck via a throwaway `packages/ui/tsconfig.checktests.json` with
  `"exclude": []`, because `tsconfig.json` excludes `src/**/*.test.ts` and vitest does not
  typecheck, so test-file type errors pass both normal gates. Baseline: 2 pre-existing errors
  in `DocPalette.test.ts` and `CommentGutter.test.ts`.
- `make test-e2e` for the Playwright additions. Noted explicitly because it rebuilds the
  frontend dist and an `e2e-server` binary.
- No Go changes: no `go test`, no Go lint, no `make api-generate`.

## Out of scope (deliberately)

- **Viewing references outside the diff.** Still count-only; the user has explicitly deferred
  this ("a bridge i don't think i want to cross just yet"). It needs a read-only file viewer.
- A Forward button, or any bidirectional history.
- Fuzzy completion or symbol suggestions in the search input. It is a plain text field.
- A keyboard-shortcut help surface for the diff view. None exists; inventing one is its own
  change.
- Migrating the five other jump-to-line features onto a shared navigation history.
- Making single-letter shortcuts modal-aware.

## Process

Architectural path: this spec, then `writing-plans` for the step-level plan (kept in scratch,
not committed, per the standing preference), then `subagent-driven-development` for the nine
tasks. Sonnet implementers and task reviewers; opus for the final whole-branch review.

Branch: a new branch off `main`, which the user has authorised. `main` is at merge commit
`2444163` (PR #13) and every other local branch is 0 commits ahead of it — `git cherry`
confirms even `docs-full-review`'s five commits have equivalents in `main` — so there is no
parked work to stack on this time. No push, PR, or merge without explicit approval.

## Amendments after implementation

Everything above describes the shipped code. This section records what changed from the
originally-approved design and why, because two of the three were defects in this document
rather than in the implementation, and the reasoning is the part worth keeping.

### 1. The departure point is the most deliberate position, not the viewport

**Originally specified:** `currentDiffPosition()` returns "the topmost at-least-partially-visible
anchored line", and every jump pushes that.

**Why that was wrong:** `flashDiffLine` centres its target. Topmost-visible and centred differ by
half a viewport, so every jump recorded a point well above where the jump had just put the reader.
Back returned to a line they had never chosen — consistently, since the same target always yields
the same viewport, which made it read as an arbitrary constant offset. The same mismatch silently
disabled `jumpTo`'s self-reference guard: the captured line could never equal the line just jumped
to, so clicking the row you were already parked on pushed a duplicate every time.

A second, independent defect surfaced once an e2e assertion finally checked *where* Back landed:
`flashDiffLine` applies its highlight synchronously but scrolls with `behavior: "smooth"`, so a
second ref click a few hundred milliseconds later read the viewport mid-animation and recorded a
line that was never on screen at rest.

**Shipped rule** — resolve the departure point by how deliberate it is:

1. **The search's origin** — the symbol a selection-initiated search was launched from. Read from
   `data-anchor-line`, so no geometry is involved.
2. **The last jump's landing** — `highlightedDiffPosition()`, read from the element
   `flashDiffLine` already tracks. Synchronous, so an in-flight scroll cannot race it.
3. **The viewport midline** — `currentDiffPosition()`, now matching `block: "center"` rather than
   the top edge, falling back to the last line above the midline when nothing reaches it.

The ordering is the design, not an implementation detail: a scroll offset is not a place a person
can name. Scrolling is imprecise and where it lands is not legible to whoever is reading the diff,
so it is only ever the fallback for having nothing better. **Do not "fix" this to track manual
scrolling** — a reader who jumps and then scrolls away is still returned to the jump target, and
that is intended.

### 2. `DiffFile.svelte` is in scope after all

**Originally specified:** "The selection-side Refs button in `DiffFile` is untouched."

That held until the origin existed. `computeSymbolSelection()` already resolved and validated the
selection's line and side and then discarded them — its own comment said there was "no use for the
side it was selected on". It now reports `{ symbol, line, side }`, and the selection-side Refs
button hands that to the store as the search's origin. `liveSymbolSelection` became derived from
that record rather than a second piece of state, so the text and its position cannot drift apart.

The origin is read without being consumed, so a jump that resolves `"missing"` — which never moves
the reader — keeps the launch point for the next valid row. It is cleared by `openBlank()`, since
the toolbar button and the `s` key know no launch point and a stale origin would send Back to an
unrelated symbol, and by `close()`. It deliberately survives `search()`: re-querying from the
gutter's own input does not change where the reader began.

### 3. Smaller corrections from the whole-branch review

- A jump whose outcome is `"missing"` no longer pushes. The reader never moved, so the entry would
  have enabled Back on a return to where they already stood.
- The duplicate-push guard compares `side` as well as path and line: an old-file deletion at line
  40 and the new file's line 40 are different places.
- A popped `LEFT`-side target that does not resolve is reported rather than failing silently.
  `requestRevealLine` is side-blind and `CollapsedRegion` only ever emits `anchorSide="RIGHT"`, so
  an unrendered LEFT line can never be revealed; the stale reveal target is cleared too.
- The gutter's focus effect seeds its guard from the store rather than from `-1`, so only an actual
  `openBlank()` focuses the input. Firing on every mount meant opening the gutter from a text
  selection stole focus, and `DiffView`'s keydown handler ignores keys while an `INPUT` is focused
  — silently swallowing `j`/`k`/`[`/`]`/`m`/`s` until the reader clicked away.
- The count badge is hidden in the `"prompt"` state; stale validation and Back notices are cleared
  when a search commits a new query; `submitSearch` applies the same empty-SHA gate as the other
  two entry points; the `s` gate is a condition on its block rather than a `return` from the whole
  handler; the two notice elements carry `role="status"`.

### 4. Testing lessons this feature paid for

- **Every geometry fixture must span the container.** The original `currentDiffPosition` tests
  crowded their lines into the container's top fifth, where "topmost" and "centred" are
  indistinguishable — which is how the wrong definition passed nine reviews.
- **Assert where a jump lands, not that something moved.** "The highlight moved off the second
  landing" passed happily while Back was returning to the wrong line. The round-trip assertion —
  the position recorded when leaving the first landing must *be* the first landing — is what caught
  the smooth-scroll race.
- **`bun run playwright test` alone tests a stale frontend.** The built frontend is embedded in the
  binary, so the e2e server serves whatever `make frontend` last produced. Skipping `make test-e2e`
  to avoid a rebuild produced a failure that looked exactly like a real bug.
