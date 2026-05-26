# PR Focus Mode Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make every panel/section around the PR diff collapsible with persisted state, add a "consolidate top sections" treatment that folds the four stacked banners into a one-line pill strip, and fix the outer sidebar strip's invisible collapse handle.

**Architecture:** Each section persists its own collapsed state via `localStorage` (matching the existing `pr-cover-collapsed` / `pr-commit-msg-collapsed` precedent). The two side panels (StackSidebar, DiffSidebar) collapse to ~30px rails with rotated labels and click-to-expand. DiffSidebar also gains a resize handle reusing the outer-sidebar pattern. A new `TopSectionsStrip.svelte` component renders the consolidated pill bar; `PullDetail.svelte` conditionally renders either the four-banner stack (today's view) or the strip + a single peeked section based on a new `pr-top-sections-consolidated` flag.

**Tech Stack:** Svelte 5 (runes), Bun, vitest, TypeScript. No backend changes.

**Spec:** `docs/superpowers/specs/2026-05-23-pr-focus-mode-design.md`
**Mockup:** `docs/superpowers/specs/2026-05-23-pr-focus-mode-mockup.html`

---

## File Map

**New files:**
- `packages/ui/src/components/detail/TopSectionsStrip.svelte` — the consolidated pill bar.
- `packages/ui/src/components/detail/TopSectionsStrip.test.ts` — component tests.

**Modified files:**
- `packages/ui/src/components/diff/PatchsetPicker.svelte` — add collapse chevron + persisted state.
- `packages/ui/src/components/detail/ReviewBriefCard.svelte` — persist existing expanded state.
- `packages/ui/src/components/detail/StackSidebar.svelte` — collapse-to-rail.
- `packages/ui/src/components/diff/DiffSidebar.svelte` — collapse-to-rail + resize handle.
- `frontend/src/lib/components/layout/AppHeader.svelte` — add always-visible collapse chevron on the sidebar's right edge when expanded.
- `frontend/src/App.svelte` — render the new chevron alongside the existing layout.
- `packages/ui/src/components/detail/PullDetail.svelte` — wrap the four top sections; render either the stack or the strip based on `pr-top-sections-consolidated`.

**Modified test files (existing stores need stub touchups if new context fields are read):**
- `packages/ui/src/components/diff/PatchsetPicker.test.ts` — new file (no existing test).
- `packages/ui/src/components/detail/ReviewBriefCard.test.ts` — new file (no existing test).
- `packages/ui/src/components/detail/StackSidebar.test.ts` — new file (no existing test).
- Possible touch-ups in `DiffFile.test.ts` / `RenderedMarkdownView.test.ts` if they break under the new DiffSidebar collapse logic (unlikely; flag if hit).

---

## Conventions

- Always commit at the end of each task (per CLAUDE.md "Commit every turn").
- Never amend; new commit per task.
- Never change branches without permission. The branch is `pr-focus`.
- Never bypass pre-commit hooks (no `--no-verify`).
- Bun, not npm. Vitest runs from `frontend/` (the vite config lives there); `bun run typecheck` runs from `packages/ui` AND `frontend/`.
- `localStorage` reads/writes wrapped in `try/catch` per existing convention.
- All new `localStorage` keys global (one user, one device). Format: `pr-<surface>-collapsed`, `pr-<surface>-width`.

---

## Persistence keys introduced

| Key | Type | Default |
|---|---|---|
| `pr-patchset-collapsed` | `"true"` / absent | absent (= false) |
| `pr-brief-collapsed` | `"true"` / absent | absent (= false) |
| `pr-stack-sidebar-collapsed` | `"true"` / absent | absent (= false) |
| `pr-review-nav-collapsed` | `"true"` / absent | absent (= false) |
| `pr-review-nav-width` | integer px as string | absent → `280` |
| `pr-top-sections-consolidated` | `"true"` / absent | absent (= false) |

---

## Task 1: Persisted collapse for PatchsetPicker

**Files:**
- Modify: `packages/ui/src/components/diff/PatchsetPicker.svelte`
- Create: `packages/ui/src/components/diff/PatchsetPicker.test.ts`

- [ ] **Step 1.1: Write the failing test**

Create `packages/ui/src/components/diff/PatchsetPicker.test.ts`:

```ts
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/svelte";
import { STORES_KEY } from "../../context.js";
import PatchsetPicker from "./PatchsetPicker.svelte";

function diffStub() {
  return {
    getPatchsets: () => [
      { id: 1, number: 1, head_sha: "a", base_sha: "x", merge_base_sha: "x", observed_at: "2026-05-01T00:00:00Z" },
      { id: 2, number: 2, head_sha: "b", base_sha: "a", merge_base_sha: "x", observed_at: "2026-05-02T00:00:00Z" },
    ],
    isPatchsetsLoading: () => false,
    getPatchsetsError: () => null,
    getScope: () => ({ kind: "head" }),
    loadPatchsets: vi.fn(async () => {}),
    resetToHead: vi.fn(),
    selectPatchsets: vi.fn(),
  };
}

function renderPicker() {
  return render(PatchsetPicker, {
    context: new Map<symbol, unknown>([[STORES_KEY, { diff: diffStub() }]]),
  });
}

beforeEach(() => {
  localStorage.clear();
});

afterEach(() => {
  cleanup();
});

describe("PatchsetPicker collapse", () => {
  it("renders chips by default (not collapsed)", () => {
    const { container } = renderPicker();
    expect(container.querySelector(".ps-picker__chips")).toBeTruthy();
  });

  it("clicking the chevron toggles collapsed state and persists", async () => {
    renderPicker();
    const chevron = screen.getByLabelText(/collapse patchsets|expand patchsets/i);
    await fireEvent.click(chevron);
    expect(localStorage.getItem("pr-patchset-collapsed")).toBe("true");
  });

  it("renders without chips when pr-patchset-collapsed=true", () => {
    localStorage.setItem("pr-patchset-collapsed", "true");
    const { container } = renderPicker();
    expect(container.querySelector(".ps-picker__chips")).toBeNull();
  });
});
```

- [ ] **Step 1.2: Run test to confirm it fails**

```bash
cd /home/orenleiman/co/middleman/frontend && bun run vitest run ../packages/ui/src/components/diff/PatchsetPicker.test.ts
```
Expected: FAIL — no chevron exists; the `.ps-picker__chips` is always rendered.

- [ ] **Step 1.3: Add collapse state + chevron + persistence to `PatchsetPicker.svelte`**

Open `packages/ui/src/components/diff/PatchsetPicker.svelte`. Below the existing `selectedNumber` / `baseNumber` `$state` declarations (around line 22-23), add:

```ts
  let collapsed = $state(
    typeof localStorage !== "undefined" && localStorage.getItem("pr-patchset-collapsed") === "true",
  );
  function toggleCollapsed(): void {
    collapsed = !collapsed;
    try {
      localStorage.setItem("pr-patchset-collapsed", String(collapsed));
    } catch { /* ignore */ }
  }
```

Find the existing rendered block at line ~123 (the `<div class="ps-picker" role="toolbar" …>`). Replace it with:

```svelte
{#if loading}
  <div class="ps-picker ps-picker--idle">Loading patchsets…</div>
{:else if errorMsg}
  <div class="ps-picker ps-picker--error">Failed to load patchsets: {errorMsg}</div>
{:else if patchsets && patchsets.length > 1}
  <div class="ps-picker" role="toolbar" aria-label="Patchsets">
    <button
      type="button"
      class="ps-picker__chevron-btn"
      onclick={toggleCollapsed}
      aria-label={collapsed ? "Expand patchsets" : "Collapse patchsets"}
      title={collapsed ? "Expand patchsets" : "Collapse patchsets"}
    >
      <svg
        class="ps-picker__chevron"
        class:ps-picker__chevron--collapsed={collapsed}
        width="10" height="10" viewBox="0 0 10 10" fill="none"
        stroke="currentColor" stroke-width="1.6"
      >
        <polyline points="2,3.5 5,6.5 8,3.5" stroke-linecap="round" stroke-linejoin="round" />
      </svg>
    </button>
    <span class="ps-picker__label">Patchsets</span>
    {#if !collapsed}
      <div class="ps-picker__chips">
        <!-- KEEP the existing chip rendering loop and ✕-reset button as-is.
             Move them inside this conditional. The exact markup is whatever
             was there before — preserve it byte-for-byte. -->
      </div>
    {/if}
  </div>
{/if}
```

Important: this step replaces the *outer* rendering structure but leaves the chip-loop / ✕ reset markup unchanged. Open the file, read what's inside the existing `<div class="ps-picker__chips">` block, and paste it back verbatim into the new `{#if !collapsed}` branch.

Then at the bottom of the `<style>` block, append:

```css
  .ps-picker__chevron-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 20px;
    height: 20px;
    border-radius: var(--radius-sm);
    background: none;
    border: none;
    color: var(--text-muted);
    cursor: pointer;
  }

  .ps-picker__chevron-btn:hover {
    background: var(--bg-surface-hover);
    color: var(--text-primary);
  }

  .ps-picker__chevron {
    transition: transform 0.15s;
  }

  .ps-picker__chevron--collapsed {
    transform: rotate(-90deg);
  }
```

- [ ] **Step 1.4: Run tests to confirm pass**

```bash
cd /home/orenleiman/co/middleman/frontend && bun run vitest run ../packages/ui/src/components/diff/PatchsetPicker.test.ts
cd /home/orenleiman/co/middleman/packages/ui && bun run typecheck
```
Expected: 3 tests pass, 0 type errors.

- [ ] **Step 1.5: Commit**

```bash
git add packages/ui/src/components/diff/PatchsetPicker.svelte \
        packages/ui/src/components/diff/PatchsetPicker.test.ts
git commit -m "$(cat <<'EOF'
feat(ui): persisted collapse for PatchsetPicker

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: Persisted collapse for ReviewBriefCard

**Files:**
- Modify: `packages/ui/src/components/detail/ReviewBriefCard.svelte`
- Create: `packages/ui/src/components/detail/ReviewBriefCard.test.ts`

`ReviewBriefCard` already has a chevron + in-memory `expanded` state (line 23: `let expanded = $state(false);`). Convert it to a persisted `collapsed` model so it survives reloads. Note the polarity flip: today it tracks `expanded`, we'll switch to `collapsed` for parity with the other banners. Default value when no key is present: collapsed=false (i.e. visible).

- [ ] **Step 2.1: Write the failing test**

Create `packages/ui/src/components/detail/ReviewBriefCard.test.ts`:

```ts
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/svelte";
import { STORES_KEY } from "../../context.js";
import ReviewBriefCard from "./ReviewBriefCard.svelte";

function aiStub() {
  return {
    getBriefs: () => [],
    getActiveBriefForPR: () => null,
    createBrief: vi.fn(),
    cancelBrief: vi.fn(),
    deleteBrief: vi.fn(),
  };
}

function detailStub() {
  return {
    getDetail: () => null,
    getDetailLoaded: () => true,
  };
}

function renderCard() {
  return render(ReviewBriefCard, {
    props: { owner: "acme", name: "widget", number: 1 },
    context: new Map<symbol, unknown>([
      [STORES_KEY, { ai: aiStub(), detail: detailStub() }],
    ]),
  });
}

beforeEach(() => {
  localStorage.clear();
});

afterEach(() => {
  cleanup();
});

describe("ReviewBriefCard collapse persistence", () => {
  it("clicking the chevron persists pr-brief-collapsed", async () => {
    renderCard();
    const toggle = screen.getByRole("button", { name: /brief|toggle/i });
    await fireEvent.click(toggle);
    expect(localStorage.getItem("pr-brief-collapsed")).not.toBeNull();
  });

  it("respects pr-brief-collapsed=true on first render", () => {
    localStorage.setItem("pr-brief-collapsed", "true");
    const { container } = renderCard();
    expect(container.querySelector(".brief__body")).toBeNull();
  });
});
```

Adjust the button name regex once you read the actual `aria-label` on the existing toggle button in `ReviewBriefCard.svelte:162-170`.

- [ ] **Step 2.2: Run test to confirm it fails**

```bash
cd /home/orenleiman/co/middleman/frontend && bun run vitest run ../packages/ui/src/components/detail/ReviewBriefCard.test.ts
```
Expected: FAIL — `pr-brief-collapsed` is never set.

- [ ] **Step 2.3: Convert ReviewBriefCard's `expanded` state to persisted `collapsed`**

In `packages/ui/src/components/detail/ReviewBriefCard.svelte`, replace the line `let expanded = $state(false);` (around line 23) with:

```ts
  let collapsed = $state(
    typeof localStorage !== "undefined" && localStorage.getItem("pr-brief-collapsed") === "true",
  );
  function toggleCollapsed(): void {
    collapsed = !collapsed;
    try {
      localStorage.setItem("pr-brief-collapsed", String(collapsed));
    } catch { /* ignore */ }
  }
  // Backwards-compatible alias for the existing template — `expanded` is
  // the inverse of `collapsed`. Avoids a sweeping rename of every callsite.
  const expanded = $derived(!collapsed);
```

Find the existing `onclick` on the toggle button (around line 162) — it likely calls something like `() => expanded = !expanded`. Replace that with `onclick={toggleCollapsed}`. Make sure no other site assigns to `expanded` (it's now derived and read-only).

- [ ] **Step 2.4: Run tests + typecheck**

```bash
cd /home/orenleiman/co/middleman/frontend && bun run vitest run ../packages/ui/src/components/detail/ReviewBriefCard.test.ts
cd /home/orenleiman/co/middleman/packages/ui && bun run typecheck
```
Expected: 2 tests pass, 0 type errors. If typecheck complains that `expanded` is no longer assignable, that's exactly the situation `$derived(!collapsed)` is meant to handle — confirm there are no remaining `expanded = ...` assignments anywhere in the file.

- [ ] **Step 2.5: Commit**

```bash
git add packages/ui/src/components/detail/ReviewBriefCard.svelte \
        packages/ui/src/components/detail/ReviewBriefCard.test.ts
git commit -m "$(cat <<'EOF'
feat(ui): persist ReviewBriefCard collapse state

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: StackSidebar collapse-to-rail

**Files:**
- Modify: `packages/ui/src/components/detail/StackSidebar.svelte`
- Create: `packages/ui/src/components/detail/StackSidebar.test.ts`

- [ ] **Step 3.1: Write the failing test**

Create `packages/ui/src/components/detail/StackSidebar.test.ts`:

```ts
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/svelte";
import { STORES_KEY, API_CLIENT_KEY, NAVIGATE_KEY } from "../../context.js";
import StackSidebar from "./StackSidebar.svelte";

function clientStub() {
  return {
    GET: vi.fn(async () => ({
      data: {
        in_stack: true,
        stack_id: 1,
        stack_name: "demo-stack",
        position: 1,
        size: 3,
        health: "ok",
        members: [
          { number: 1, title: "PR 1", state: "open", ci_status: "success",
            review_decision: "APPROVED", position: 1, is_draft: false,
            base_branch: "main", blocked_by: null },
        ],
      },
      error: undefined,
    })),
  };
}

function syncStub() {
  return { subscribeSyncComplete: () => () => {} };
}

function renderSidebar() {
  return render(StackSidebar, {
    props: { owner: "acme", name: "widget", number: 1 },
    context: new Map<symbol, unknown>([
      [STORES_KEY, { sync: syncStub() }],
      [API_CLIENT_KEY, clientStub()],
      [NAVIGATE_KEY, vi.fn()],
    ]),
  });
}

beforeEach(() => {
  localStorage.clear();
});

afterEach(() => {
  cleanup();
});

describe("StackSidebar collapse", () => {
  it("clicking the collapse button writes pr-stack-sidebar-collapsed", async () => {
    renderSidebar();
    // Allow fetchStack to resolve.
    await new Promise((r) => setTimeout(r, 0));
    const toggle = await screen.findByLabelText(/collapse stack|expand stack/i);
    await fireEvent.click(toggle);
    expect(localStorage.getItem("pr-stack-sidebar-collapsed")).toBe("true");
  });

  it("renders as a rail when pr-stack-sidebar-collapsed=true", async () => {
    localStorage.setItem("pr-stack-sidebar-collapsed", "true");
    const { container } = renderSidebar();
    await new Promise((r) => setTimeout(r, 0));
    expect(container.querySelector(".stack-sidebar--rail")).toBeTruthy();
  });
});
```

- [ ] **Step 3.2: Run test to confirm it fails**

```bash
cd /home/orenleiman/co/middleman/frontend && bun run vitest run ../packages/ui/src/components/detail/StackSidebar.test.ts
```
Expected: FAIL — no collapse button, no `.stack-sidebar--rail`.

- [ ] **Step 3.3: Add collapse-to-rail to `StackSidebar.svelte`**

In `packages/ui/src/components/detail/StackSidebar.svelte`, below the existing `data`/`visible`/`requestSeq` state (around line 40), add:

```ts
  let collapsed = $state(
    typeof localStorage !== "undefined" && localStorage.getItem("pr-stack-sidebar-collapsed") === "true",
  );
  function toggleCollapsed(): void {
    collapsed = !collapsed;
    try {
      localStorage.setItem("pr-stack-sidebar-collapsed", String(collapsed));
    } catch { /* ignore */ }
  }
```

Find the outermost `<aside>` (or equivalent) wrapper in the template. Locate it via the existing `class:` bindings and the gated `{#if visible}` block. Modify the wrapper so that when `collapsed` is true, the content is replaced with a rail:

```svelte
{#if visible && data}
  {#if collapsed}
    <button
      type="button"
      class="stack-sidebar stack-sidebar--rail"
      onclick={toggleCollapsed}
      aria-label="Expand stack sidebar"
      title="Expand stack: {data.stack_name} ({data.size} PRs)"
    >
      <span class="stack-sidebar__rail-label">Stack: {data.stack_name} · {data.size} PRs</span>
    </button>
  {:else}
    <aside class="stack-sidebar">
      <button
        type="button"
        class="stack-sidebar__collapse"
        onclick={toggleCollapsed}
        aria-label="Collapse stack sidebar"
        title="Collapse stack sidebar"
      >
        <!-- chevron icon -->
        <svg width="10" height="10" viewBox="0 0 10 10" fill="none"
             stroke="currentColor" stroke-width="1.6">
          <polyline points="6.5,2 3.5,5 6.5,8" stroke-linecap="round" stroke-linejoin="round" />
        </svg>
      </button>
      <!-- KEEP the existing stack-listing markup here, byte-for-byte. -->
    </aside>
  {/if}
{/if}
```

When you implement: open the file, identify the current outer `<aside>` and the body markup that lives inside it, then wrap with the `{#if collapsed}` / `{:else}` branches as shown. Preserve all existing children verbatim in the `{:else}` branch. Add an absolutely-positioned `.stack-sidebar__collapse` button inside the existing wrapper (top-right or top-left as makes sense for the existing layout).

Add the rail styles in the `<style>` block:

```css
  .stack-sidebar--rail {
    width: 30px;
    min-height: 200px;
    border: 1px solid var(--border-default);
    border-radius: var(--radius-sm);
    background: var(--bg-inset);
    color: var(--text-muted);
    cursor: pointer;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 6px 0;
  }

  .stack-sidebar--rail:hover {
    background: var(--bg-surface-hover);
    color: var(--text-primary);
  }

  .stack-sidebar__rail-label {
    writing-mode: vertical-rl;
    transform: rotate(180deg);
    text-orientation: mixed;
    font-size: 10px;
    white-space: nowrap;
  }

  .stack-sidebar__collapse {
    position: absolute;
    top: 6px;
    right: 6px;
    width: 18px;
    height: 18px;
    border-radius: var(--radius-sm);
    background: none;
    border: none;
    color: var(--text-muted);
    cursor: pointer;
  }

  .stack-sidebar__collapse:hover {
    background: var(--bg-surface-hover);
    color: var(--text-primary);
  }
```

If the existing `.stack-sidebar` doesn't currently have `position: relative`, add it so the absolute-positioned `.stack-sidebar__collapse` anchors correctly.

- [ ] **Step 3.4: Run tests + typecheck**

```bash
cd /home/orenleiman/co/middleman/frontend && bun run vitest run ../packages/ui/src/components/detail/StackSidebar.test.ts
cd /home/orenleiman/co/middleman/packages/ui && bun run typecheck
```
Expected: 2 tests pass, 0 type errors.

- [ ] **Step 3.5: Commit**

```bash
git add packages/ui/src/components/detail/StackSidebar.svelte \
        packages/ui/src/components/detail/StackSidebar.test.ts
git commit -m "$(cat <<'EOF'
feat(ui): collapse-to-rail for StackSidebar

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: DiffSidebar collapse-to-rail

**Files:**
- Modify: `packages/ui/src/components/diff/DiffSidebar.svelte`
- Modify: `packages/ui/src/components/detail/PullDetail.svelte` (the parent that owns the `.files-sidebar` wrapper)
- Modify: any existing `DiffSidebar` tests (extend rather than overwrite) — see Step 4.1.

The `DiffSidebar` is mounted inside `<aside class="files-sidebar">` in `PullDetail.svelte:365-367`. The width is fixed at 280px via CSS. We'll add a collapse-to-rail behavior driven from a store reader added to the existing diff store (no new store; just thread the boolean through).

Decision point: put the collapsed state directly in `DiffSidebar.svelte` like the other components in this plan, OR in the `diff` store. For consistency with the other tasks here, keep it directly in `DiffSidebar.svelte` and have `PullDetail.svelte` import a small exported getter for the rail-rendering decision.

Concrete approach: `DiffSidebar.svelte` owns `collapsed` state and persists it. It exports nothing — `PullDetail.svelte`'s outer `<aside class="files-sidebar">` reads the same `localStorage` key directly via a tiny helper, so when collapsed the wrapper renders a 30px rail instead of the 280px column.

- [ ] **Step 4.1: Write the failing test for the rail behavior inside DiffSidebar**

There may not be an existing `DiffSidebar.test.ts`. If one exists, extend it; otherwise create `packages/ui/src/components/diff/DiffSidebar.test.ts`:

```ts
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/svelte";
import { STORES_KEY } from "../../context.js";
import DiffSidebar from "./DiffSidebar.svelte";

function diffStub() {
  return {
    getFileList: () => ({ stale: false, files: [
      { path: "a.go", status: "modified", is_binary: false,
        is_whitespace_only: false, additions: 1, deletions: 0, hunks: [] },
    ] }),
    isFileListLoading: () => false,
    getActiveFile: () => null,
    requestScrollToFile: vi.fn(),
    isFileReviewed: () => false,
    getFileReviewProgress: () => null,
    getCommits: () => [],
    getDraft: () => ({ comments: [] }),
  };
}

function detailStub() {
  return { getDetail: () => null };
}

function aiStub() {
  return {
    getThreads: () => [],
    getQuestions: () => [],
  };
}

function renderSidebar() {
  return render(DiffSidebar, {
    context: new Map<symbol, unknown>([
      [STORES_KEY, { diff: diffStub(), detail: detailStub(), ai: aiStub() }],
    ]),
  });
}

beforeEach(() => {
  localStorage.clear();
});

afterEach(() => {
  cleanup();
});

describe("DiffSidebar collapse-to-rail", () => {
  it("renders the file tree by default", () => {
    const { container } = renderSidebar();
    expect(container.querySelector(".diff-files")).toBeTruthy();
  });

  it("clicking the collapse button writes pr-review-nav-collapsed", async () => {
    renderSidebar();
    const toggle = await screen.findByLabelText(/collapse review nav|expand review nav/i);
    await fireEvent.click(toggle);
    expect(localStorage.getItem("pr-review-nav-collapsed")).toBe("true");
  });

  it("renders rail markup when pr-review-nav-collapsed=true", () => {
    localStorage.setItem("pr-review-nav-collapsed", "true");
    const { container } = renderSidebar();
    expect(container.querySelector(".diff-sidebar--rail")).toBeTruthy();
  });
});
```

The stub `diffStub` may need additional methods depending on what the existing `CommitListSection` / `PendingCommentsSection` / `QuestionsSection` children call. If the test runner reports "undefined method" errors, add the missing methods as `vi.fn(() => [])` stubs to satisfy the component graph. Do NOT exercise behavior beyond what the new collapse logic needs.

- [ ] **Step 4.2: Run test to confirm it fails**

```bash
cd /home/orenleiman/co/middleman/frontend && bun run vitest run ../packages/ui/src/components/diff/DiffSidebar.test.ts
```
Expected: FAIL.

- [ ] **Step 4.3: Add collapse-to-rail to `DiffSidebar.svelte`**

Open `packages/ui/src/components/diff/DiffSidebar.svelte`. Just below the `<script lang="ts">` and existing import block, add:

```ts
  let collapsed = $state(
    typeof localStorage !== "undefined" && localStorage.getItem("pr-review-nav-collapsed") === "true",
  );
  function toggleCollapsed(): void {
    collapsed = !collapsed;
    try {
      localStorage.setItem("pr-review-nav-collapsed", String(collapsed));
    } catch { /* ignore */ }
  }

  // Counts for the rail label
  const commitCount = $derived((diff.getCommits() ?? []).length);
  const draftCount = $derived((diff.getDraft()?.comments ?? []).length);
  // AI Q&A count via the ai store if present; default 0 if not in context.
  // Use the store wrapped in optional chaining to avoid crashing tests that
  // don't provide ai.
```

(Skip the AI count if `ai` isn't already pulled from `getStores()` — keep the rail label to commits/drafts/files only. The implementer should check what the existing script already pulls from `getStores()` and extend conservatively.)

Replace the template (the existing structure at lines 84-139 in the on-disk file) by wrapping it with the `{#if collapsed}` / `{:else}` switch:

```svelte
{#if collapsed}
  <button
    type="button"
    class="diff-sidebar--rail"
    onclick={toggleCollapsed}
    aria-label="Expand review nav"
    title="Expand review nav"
  >
    <span class="diff-sidebar__rail-label">
      {commitCount}c · {draftCount}d · {fileCount}f
    </span>
  </button>
{:else}
  <!-- KEEP the existing CommitListSection / PendingCommentsSection /
       QuestionsSection / diff-files block here verbatim. Add a small
       collapse button at the top of the column. -->
  <button
    type="button"
    class="diff-sidebar__collapse"
    onclick={toggleCollapsed}
    aria-label="Collapse review nav"
    title="Collapse review nav"
  >
    <svg width="10" height="10" viewBox="0 0 10 10" fill="none"
         stroke="currentColor" stroke-width="1.6">
      <polyline points="6.5,2 3.5,5 6.5,8" stroke-linecap="round" stroke-linejoin="round" />
    </svg>
  </button>
  <CommitListSection />
  <PendingCommentsSection />
  <QuestionsSection />
  <!-- existing <div class="diff-files"> block stays exactly as-is -->
{/if}
```

Add a `fileCount` derivation near the others (use the existing `filteredDiffFiles` if available, or `diff.getFileList()?.files?.length ?? 0`):

```ts
  const fileCount = $derived(diff.getFileList()?.files?.length ?? 0);
```

In the `<style>` block append:

```css
  .diff-sidebar--rail {
    width: 30px;
    height: 100%;
    min-height: 200px;
    border: none;
    background: var(--bg-inset);
    color: var(--text-muted);
    cursor: pointer;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 6px 0;
  }

  .diff-sidebar--rail:hover {
    background: var(--bg-surface-hover);
    color: var(--text-primary);
  }

  .diff-sidebar__rail-label {
    writing-mode: vertical-rl;
    transform: rotate(180deg);
    text-orientation: mixed;
    font-size: 10px;
    white-space: nowrap;
  }

  .diff-sidebar__collapse {
    position: sticky;
    top: 0;
    margin-left: auto;
    margin-right: 4px;
    margin-top: 4px;
    width: 18px;
    height: 18px;
    border-radius: var(--radius-sm);
    background: none;
    border: none;
    color: var(--text-muted);
    cursor: pointer;
    z-index: 1;
  }

  .diff-sidebar__collapse:hover {
    background: var(--bg-surface-hover);
    color: var(--text-primary);
  }
```

- [ ] **Step 4.4: Narrow the parent wrapper when collapsed**

Open `packages/ui/src/components/detail/PullDetail.svelte`. Find the `<aside class="files-sidebar">` block at line 365-367. We need that wrapper to shrink to ~30px when the inner state is collapsed. The cleanest path: have the wrapper class react to the same `localStorage` key.

Add a small helper in `PullDetail.svelte`'s script block (or pick up the existing imports if you want a shared module). Near the top of `<script>`:

```ts
  let reviewNavCollapsed = $state(
    typeof localStorage !== "undefined" && localStorage.getItem("pr-review-nav-collapsed") === "true",
  );

  // Listen for cross-component updates via the `storage` event so that
  // clicking the collapse inside DiffSidebar updates this wrapper width
  // instantly in same-tab usage. localStorage only fires `storage` across
  // tabs; for same-tab we listen on a custom `pr-ui-state` event that
  // DiffSidebar dispatches.
  if (typeof window !== "undefined") {
    const handler = (): void => {
      reviewNavCollapsed = localStorage.getItem("pr-review-nav-collapsed") === "true";
    };
    window.addEventListener("storage", handler);
    window.addEventListener("pr-ui-state", handler);
  }
```

In `DiffSidebar.svelte`'s `toggleCollapsed`, dispatch the event so the wrapper updates:

```ts
  function toggleCollapsed(): void {
    collapsed = !collapsed;
    try {
      localStorage.setItem("pr-review-nav-collapsed", String(collapsed));
      if (typeof window !== "undefined") {
        window.dispatchEvent(new CustomEvent("pr-ui-state"));
      }
    } catch { /* ignore */ }
  }
```

Then in the `.files-sidebar` selector in `PullDetail.svelte` (lines 859-867) add a modifier:

```css
  .files-sidebar--collapsed {
    width: 30px;
    min-width: 30px;
  }
```

And update the template:

```svelte
<aside class="files-sidebar" class:files-sidebar--collapsed={reviewNavCollapsed}>
  <DiffSidebar />
</aside>
```

- [ ] **Step 4.5: Run tests + typecheck**

```bash
cd /home/orenleiman/co/middleman/frontend && bun run vitest run ../packages/ui/src/components/diff/DiffSidebar.test.ts
cd /home/orenleiman/co/middleman/frontend && bun run vitest run ../packages/ui/src/components/diff
cd /home/orenleiman/co/middleman/packages/ui && bun run typecheck
```
Expected: 3 tests pass; full diff component suite green except for known pre-existing failures (none expected after T8 of the prior PR-thread-hiding feature fixed `DiffFile.test.ts`).

- [ ] **Step 4.6: Commit**

```bash
git add packages/ui/src/components/diff/DiffSidebar.svelte \
        packages/ui/src/components/diff/DiffSidebar.test.ts \
        packages/ui/src/components/detail/PullDetail.svelte
git commit -m "$(cat <<'EOF'
feat(ui): collapse-to-rail for review nav (DiffSidebar)

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: DiffSidebar resize handle

**Files:**
- Modify: `packages/ui/src/components/detail/PullDetail.svelte`

The handle lives on the wrapper (`.files-sidebar`), not inside `DiffSidebar.svelte`, so the width applies even when the inner content is collapsed (though in practice the collapse-rail case is fixed at 30px and the resize handle becomes a no-op).

- [ ] **Step 5.1: Write the failing behavioral test**

Add to `frontend/src/lib/components/layout/PullDetail.test.ts` (or, if no such file exists, create `packages/ui/src/components/detail/PullDetail.resize.test.ts`). The minimum behavior to assert:

```ts
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render } from "@testing-library/svelte";
import { STORES_KEY } from "../../context.js";
import PullDetail from "./PullDetail.svelte";

// PullDetail mounts a tree of detail components; this test stubs the
// minimal store surface needed to render the Review tab without crashing.
// If the implementer hits "X is not a function" errors, extend the stubs
// just enough to render — do NOT exercise unrelated behavior.

// … minimal stubs for detail, diff, ai, sync …

beforeEach(() => { localStorage.clear(); });
afterEach(() => { cleanup(); });

describe("DiffSidebar resize", () => {
  it("persists pr-review-nav-width when the handle is dragged", async () => {
    // Render, get the handle, fire pointer events that simulate a 60px
    // rightward drag, assert the stored width.
    // Use vi.spyOn(localStorage, 'setItem') as a quick observer.
    // If full simulation is brittle, assert that the resize handle element
    // exists and the width store starts at the default (280).
  });
});
```

This test is intentionally lighter than the rest because resize testing in JSDOM is fiddly. A minimal "the handle exists" + "setSidebarWidth is called on drag" check is acceptable. Cite the existing outer-sidebar resize pattern at `frontend/src/App.svelte:299` for the equivalent code path being copied.

- [ ] **Step 5.2: Run test to confirm it fails**

```bash
cd /home/orenleiman/co/middleman/frontend && bun run vitest run ../packages/ui/src/components/detail/PullDetail.resize.test.ts
```
Expected: FAIL.

- [ ] **Step 5.3: Add the resize handle**

In `packages/ui/src/components/detail/PullDetail.svelte`, near the existing state declarations, add:

```ts
  const DEFAULT_REVIEW_NAV_WIDTH = 280;
  const MIN_REVIEW_NAV_WIDTH = 180;
  const MAX_REVIEW_NAV_WIDTH = 560;

  function loadReviewNavWidth(): number {
    try {
      const raw = localStorage.getItem("pr-review-nav-width");
      if (!raw) return DEFAULT_REVIEW_NAV_WIDTH;
      const n = Number(raw);
      if (!Number.isFinite(n)) return DEFAULT_REVIEW_NAV_WIDTH;
      return Math.max(MIN_REVIEW_NAV_WIDTH, Math.min(MAX_REVIEW_NAV_WIDTH, Math.round(n)));
    } catch {
      return DEFAULT_REVIEW_NAV_WIDTH;
    }
  }

  let reviewNavWidth = $state(loadReviewNavWidth());
  function persistReviewNavWidth(w: number): void {
    try { localStorage.setItem("pr-review-nav-width", String(w)); }
    catch { /* ignore */ }
  }

  let resizing = false;
  let resizeStartX = 0;
  let resizeStartWidth = 0;

  function onResizeStart(e: PointerEvent): void {
    if (reviewNavCollapsed) return;  // no resize when collapsed
    resizing = true;
    resizeStartX = e.clientX;
    resizeStartWidth = reviewNavWidth;
    (e.target as HTMLElement).setPointerCapture(e.pointerId);
  }

  function onResizeMove(e: PointerEvent): void {
    if (!resizing) return;
    const delta = e.clientX - resizeStartX;
    const next = Math.max(
      MIN_REVIEW_NAV_WIDTH,
      Math.min(MAX_REVIEW_NAV_WIDTH, resizeStartWidth + delta),
    );
    reviewNavWidth = next;
  }

  function onResizeEnd(e: PointerEvent): void {
    if (!resizing) return;
    resizing = false;
    (e.target as HTMLElement).releasePointerCapture(e.pointerId);
    persistReviewNavWidth(reviewNavWidth);
  }
```

In the template, set the wrapper's inline width and add a handle:

```svelte
<aside
  class="files-sidebar"
  class:files-sidebar--collapsed={reviewNavCollapsed}
  style:width={reviewNavCollapsed ? "30px" : `${reviewNavWidth}px`}
>
  <DiffSidebar />
  {#if !reviewNavCollapsed}
    <div
      class="files-sidebar__resize"
      role="separator"
      aria-orientation="vertical"
      onpointerdown={onResizeStart}
      onpointermove={onResizeMove}
      onpointerup={onResizeEnd}
    ></div>
  {/if}
</aside>
```

And in CSS:

```css
  .files-sidebar {
    /* drop the fixed width: 280px line; now driven by style:width */
    flex-shrink: 0;
    border-right: 1px solid var(--border-default);
    background: var(--bg-surface);
    overflow-y: auto;
    display: flex;
    flex-direction: column;
    position: relative;
  }

  .files-sidebar__resize {
    position: absolute;
    top: 0;
    right: -3px;
    width: 6px;
    height: 100%;
    cursor: col-resize;
    background: transparent;
    z-index: 2;
  }

  .files-sidebar__resize:hover {
    background: var(--accent-blue);
    opacity: 0.4;
  }
```

Remove the previous static `width: 280px` line from `.files-sidebar` since width is now inline.

- [ ] **Step 5.4: Run tests + typecheck**

```bash
cd /home/orenleiman/co/middleman/frontend && bun run vitest run ../packages/ui/src/components/detail/PullDetail.resize.test.ts
cd /home/orenleiman/co/middleman/packages/ui && bun run typecheck
```
Expected: PASS, 0 type errors.

- [ ] **Step 5.5: Commit**

```bash
git add packages/ui/src/components/detail/PullDetail.svelte \
        packages/ui/src/components/detail/PullDetail.resize.test.ts
git commit -m "$(cat <<'EOF'
feat(ui): resize handle for review nav (DiffSidebar) wrapper

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: Outer sidebar strip — visible collapse chevron

**Files:**
- Modify: `frontend/src/lib/components/layout/AppHeader.svelte` OR `frontend/src/App.svelte` (whichever owns the expanded-sidebar rendering — check both before editing).
- Modify: `frontend/src/lib/components/layout/AppHeader.test.ts` (extend, do not overwrite).

Today the toggle button only renders when the sidebar is already collapsed (`AppHeader.svelte:36-50`). Add a symmetric "collapse" chevron when the sidebar is expanded, anchored to the right edge of the sidebar.

The expanded sidebar lives in `frontend/src/App.svelte` (around the `getSidebarWidth` references at lines 33-40). Open `App.svelte` and find where the sidebar strip is rendered — it's a sibling of the main content area, controlled by `isSidebarCollapsed()`. Add a small chevron button on the right edge of that sidebar's wrapper.

- [ ] **Step 6.1: Write the failing test**

Extend `frontend/src/lib/components/layout/AppHeader.test.ts` (or add a new file `frontend/src/App.collapse-chevron.test.ts` if the test lives more naturally there). The test:

```ts
import { describe, expect, it } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
// … existing mocks for @middleman/ui, runtime, etc. — see existing AppHeader.test.ts pattern

describe("expanded sidebar collapse chevron", () => {
  it("renders a 'Collapse sidebar' control when the sidebar is expanded", () => {
    // Force isSidebarCollapsed=false via the existing store-init seam.
    // Render the parent that owns the sidebar (App.svelte), assert the
    // chevron exists by aria-label.
  });

  it("clicking the chevron collapses the sidebar", async () => {
    // Render, click, observe localStorage('middleman-sidebar') === 'collapsed'.
  });
});
```

This test is intentionally light. If `App.svelte`'s render dependencies make it impractical to mount in a unit test, the implementer can instead add the test to whichever component actually owns the chevron rendering. The point is: a regression that removes the chevron must be caught.

- [ ] **Step 6.2: Run test to confirm it fails**

```bash
cd /home/orenleiman/co/middleman/frontend && bun run vitest run src/lib/components/layout/AppHeader.test.ts
```
Expected: FAIL on the new assertions.

- [ ] **Step 6.3: Add the chevron**

Open the file that renders the expanded sidebar (most likely `frontend/src/App.svelte`; check by grepping for `getSidebarWidth` and `<aside`). Inside or adjacent to the sidebar's right edge, add a button:

```svelte
{#if !isSidebarCollapsed() && isSidebarToggleEnabled()}
  <button
    type="button"
    class="sidebar-collapse-chevron"
    onclick={toggleSidebar}
    aria-label="Collapse sidebar"
    title="Collapse sidebar"
  >
    <svg width="10" height="10" viewBox="0 0 10 10" fill="none"
         stroke="currentColor" stroke-width="1.6">
      <polyline points="6.5,2 3.5,5 6.5,8" stroke-linecap="round" stroke-linejoin="round" />
    </svg>
  </button>
{/if}
```

Place the button absolutely-positioned at the right edge of the sidebar wrapper, vertically centered. CSS:

```css
  .sidebar-collapse-chevron {
    position: absolute;
    right: -10px;  /* straddle the sidebar's right border */
    top: 50%;
    transform: translateY(-50%);
    width: 18px;
    height: 32px;
    border-radius: var(--radius-sm);
    border: 1px solid var(--border-default);
    background: var(--bg-surface);
    color: var(--text-muted);
    cursor: pointer;
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 10;
  }

  .sidebar-collapse-chevron:hover {
    color: var(--text-primary);
    background: var(--bg-surface-hover);
  }
```

The sidebar wrapper must have `position: relative` for the absolute positioning to anchor. Confirm by reading the existing CSS and adding `position: relative` if missing.

- [ ] **Step 6.4: Run tests + typecheck**

```bash
cd /home/orenleiman/co/middleman/frontend && bun run vitest run src/lib/components/layout/AppHeader.test.ts
cd /home/orenleiman/co/middleman/frontend && bun run typecheck
```
Expected: PASS, 0 type errors.

- [ ] **Step 6.5: Commit**

```bash
git add frontend/src/App.svelte \
        frontend/src/lib/components/layout/AppHeader.svelte \
        frontend/src/lib/components/layout/AppHeader.test.ts
git commit -m "$(cat <<'EOF'
fix(ui): always-visible collapse chevron on expanded sidebar strip

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

(Drop any unstaged file from the `git add` line if you didn't actually touch it.)

---

## Task 7: TopSectionsStrip + consolidation wiring

**Files:**
- Create: `packages/ui/src/components/detail/TopSectionsStrip.svelte`
- Create: `packages/ui/src/components/detail/TopSectionsStrip.test.ts`
- Modify: `packages/ui/src/components/detail/PullDetail.svelte`

This is the new visual element. When `pr-top-sections-consolidated=true`, render the strip in place of the four banners. When the user clicks a pip, that single section expands inline below the strip (ephemeral, in-memory peek state — one peek at a time). The leading chevron exits consolidation.

The "Consolidate" affordance is a small chip pinned to the top-right corner of a new wrapper around the four banners. The chip is the only chrome added in non-consolidated mode.

- [ ] **Step 7.1: Write the failing test**

Create `packages/ui/src/components/detail/TopSectionsStrip.test.ts`:

```ts
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/svelte";
import TopSectionsStrip from "./TopSectionsStrip.svelte";

interface Props {
  // The strip is parent-agnostic; the parent wires it to actual banners.
  // For tests, we mount it directly with no children.
  onExpandAll: () => void;
  onPeek: (id: string) => void;
  peeked: string | null;
  // each pip describes its section
  pips: Array<{ id: string; label: string; muted: boolean }>;
}

beforeEach(() => { localStorage.clear(); });
afterEach(() => { cleanup(); });

describe("TopSectionsStrip", () => {
  it("renders one pip per section", () => {
    const props: Props = {
      onExpandAll: vi.fn(),
      onPeek: vi.fn(),
      peeked: null,
      pips: [
        { id: "cover", label: "cover", muted: false },
        { id: "msg", label: "message", muted: true },
        { id: "patchset", label: "patchset 2/3", muted: true },
        { id: "brief", label: "brief", muted: true },
      ],
    };
    render(TopSectionsStrip, { props });
    expect(screen.getByText("cover")).toBeTruthy();
    expect(screen.getByText("message")).toBeTruthy();
    expect(screen.getByText("patchset 2/3")).toBeTruthy();
    expect(screen.getByText("brief")).toBeTruthy();
  });

  it("clicking a pip calls onPeek with the id", async () => {
    const onPeek = vi.fn();
    render(TopSectionsStrip, {
      props: {
        onExpandAll: vi.fn(),
        onPeek,
        peeked: null,
        pips: [{ id: "cover", label: "cover", muted: false }],
      },
    });
    await fireEvent.click(screen.getByText("cover"));
    expect(onPeek).toHaveBeenCalledWith("cover");
  });

  it("clicking the leading chevron calls onExpandAll", async () => {
    const onExpandAll = vi.fn();
    render(TopSectionsStrip, {
      props: {
        onExpandAll,
        onPeek: vi.fn(),
        peeked: null,
        pips: [{ id: "cover", label: "cover", muted: false }],
      },
    });
    await fireEvent.click(screen.getByLabelText("Expand all sections"));
    expect(onExpandAll).toHaveBeenCalled();
  });

  it("marks the peeked pip with a peeked class", () => {
    const { container } = render(TopSectionsStrip, {
      props: {
        onExpandAll: vi.fn(),
        onPeek: vi.fn(),
        peeked: "cover",
        pips: [
          { id: "cover", label: "cover", muted: false },
          { id: "msg", label: "message", muted: true },
        ],
      },
    });
    expect(container.querySelector('[data-id="cover"].pip--peeked')).toBeTruthy();
    expect(container.querySelector('[data-id="msg"].pip--peeked')).toBeNull();
  });
});
```

- [ ] **Step 7.2: Run test to confirm it fails**

```bash
cd /home/orenleiman/co/middleman/frontend && bun run vitest run ../packages/ui/src/components/detail/TopSectionsStrip.test.ts
```
Expected: FAIL — file doesn't exist.

- [ ] **Step 7.3: Implement `TopSectionsStrip.svelte`**

Create `packages/ui/src/components/detail/TopSectionsStrip.svelte`:

```svelte
<script lang="ts">
  interface Pip {
    id: string;
    label: string;
    muted: boolean;
  }
  interface Props {
    pips: Pip[];
    peeked: string | null;
    onPeek: (id: string) => void;
    onExpandAll: () => void;
  }
  const { pips, peeked, onPeek, onExpandAll }: Props = $props();
</script>

<div class="top-strip" role="toolbar" aria-label="Consolidated top sections">
  <button
    type="button"
    class="top-strip__chevron"
    onclick={onExpandAll}
    aria-label="Expand all sections"
    title="Expand all sections"
  >
    <svg width="10" height="10" viewBox="0 0 10 10" fill="none"
         stroke="currentColor" stroke-width="1.6">
      <polyline points="3.5,2 6.5,5 3.5,8" stroke-linecap="round" stroke-linejoin="round" />
    </svg>
  </button>
  {#each pips as pip (pip.id)}
    <button
      type="button"
      data-id={pip.id}
      class="pip"
      class:pip--muted={pip.muted}
      class:pip--peeked={peeked === pip.id}
      onclick={() => onPeek(pip.id)}
      aria-pressed={peeked === pip.id}
    >
      {pip.label}
    </button>
  {/each}
</div>

<style>
  .top-strip {
    display: flex;
    gap: 8px;
    align-items: center;
    padding: 6px 12px;
    background: var(--bg-inset);
    border: 1px dashed var(--accent-blue);
    border-radius: 999px;
    margin: 4px 0;
  }
  .top-strip__chevron {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 18px;
    height: 18px;
    border-radius: var(--radius-sm);
    background: none;
    border: none;
    color: var(--accent-blue);
    cursor: pointer;
  }
  .top-strip__chevron:hover {
    background: var(--bg-surface-hover);
  }
  .pip {
    padding: 2px 8px;
    border-radius: 999px;
    background: var(--bg-surface);
    border: 1px solid var(--border-muted);
    color: var(--text-primary);
    cursor: pointer;
    font-size: 11px;
    line-height: 1.4;
  }
  .pip:hover {
    border-color: var(--accent-blue);
  }
  .pip--muted {
    opacity: 0.55;
  }
  .pip--peeked {
    border-color: var(--accent-blue);
    background: color-mix(in srgb, var(--accent-blue) 12%, var(--bg-surface));
  }
</style>
```

- [ ] **Step 7.4: Run TopSectionsStrip tests to confirm pass**

```bash
cd /home/orenleiman/co/middleman/frontend && bun run vitest run ../packages/ui/src/components/detail/TopSectionsStrip.test.ts
cd /home/orenleiman/co/middleman/packages/ui && bun run typecheck
```
Expected: 4 tests pass, 0 type errors.

- [ ] **Step 7.5: Wire consolidation into `PullDetail.svelte`**

Open `packages/ui/src/components/detail/PullDetail.svelte`. Find the four-banner block in the Review tab (around lines 369-372: `ReviewCoverBanner`, `CommitMessageBanner`, `PatchsetPicker`, `ReviewBriefCard`). Wrap them in a new `<div class="top-sections">` container, with a chip pinned to the top-right and the strip rendered conditionally.

In the script block, add:

```ts
  import TopSectionsStrip from "./TopSectionsStrip.svelte";

  let topConsolidated = $state(
    typeof localStorage !== "undefined" && localStorage.getItem("pr-top-sections-consolidated") === "true",
  );
  let peeked = $state<string | null>(null);

  function setTopConsolidated(next: boolean): void {
    topConsolidated = next;
    peeked = null;
    try {
      localStorage.setItem("pr-top-sections-consolidated", String(next));
    } catch { /* ignore */ }
  }

  function togglePeek(id: string): void {
    peeked = peeked === id ? null : id;
  }

  // The pip set is derived from the live per-section collapsed states.
  // `muted` = the section was individually collapsed before consolidation;
  // unmuted pips were expanded sections. We read the same localStorage keys
  // the section components themselves write. This is intentionally a
  // snapshot — pips don't re-derive while consolidated because peek is
  // ephemeral.
  function readKey(k: string): boolean {
    try { return localStorage.getItem(k) === "true"; }
    catch { return false; }
  }
  const pips = $derived([
    { id: "cover",    label: "cover",      muted: readKey("pr-cover-collapsed") },
    { id: "msg",      label: "message",    muted: readKey("pr-commit-msg-collapsed") },
    { id: "patchset", label: "patchset",   muted: readKey("pr-patchset-collapsed") },
    { id: "brief",    label: "brief",      muted: readKey("pr-brief-collapsed") },
  ]);
```

In the template, replace the four-banner block:

```svelte
<div class="top-sections" class:top-sections--consolidated={topConsolidated}>
  {#if !topConsolidated}
    <button
      type="button"
      class="top-sections__consolidate"
      onclick={() => setTopConsolidated(true)}
      aria-label="Consolidate top sections"
      title="Consolidate top sections"
    >
      ⋯
    </button>
    <ReviewCoverBanner {pr} {owner} {name} />
    <CommitMessageBanner {owner} {name} {number} />
    <PatchsetPicker />
    <ReviewBriefCard {owner} {name} {number} />
  {:else}
    <TopSectionsStrip
      {pips}
      peeked={peeked}
      onPeek={togglePeek}
      onExpandAll={() => setTopConsolidated(false)}
    />
    {#if peeked === "cover"}
      <ReviewCoverBanner {pr} {owner} {name} />
    {:else if peeked === "msg"}
      <CommitMessageBanner {owner} {name} {number} />
    {:else if peeked === "patchset"}
      <PatchsetPicker />
    {:else if peeked === "brief"}
      <ReviewBriefCard {owner} {name} {number} />
    {/if}
  {/if}
</div>
```

Add styles:

```css
  .top-sections {
    position: relative;
    display: flex;
    flex-direction: column;
  }

  .top-sections__consolidate {
    position: absolute;
    top: 4px;
    right: 8px;
    z-index: 5;
    padding: 2px 8px;
    font-size: 11px;
    color: var(--text-muted);
    background: var(--bg-surface);
    border: 1px solid var(--border-muted);
    border-radius: 999px;
    cursor: pointer;
  }

  .top-sections__consolidate:hover {
    color: var(--text-primary);
    border-color: var(--accent-blue);
  }
```

- [ ] **Step 7.6: Run tests + typecheck**

```bash
cd /home/orenleiman/co/middleman/frontend && bun run vitest run ../packages/ui/src/components/detail/TopSectionsStrip.test.ts
cd /home/orenleiman/co/middleman/packages/ui && bun run typecheck
cd /home/orenleiman/co/middleman/frontend && bun run typecheck
```
Expected: all green.

- [ ] **Step 7.7: Commit**

```bash
git add packages/ui/src/components/detail/TopSectionsStrip.svelte \
        packages/ui/src/components/detail/TopSectionsStrip.test.ts \
        packages/ui/src/components/detail/PullDetail.svelte
git commit -m "$(cat <<'EOF'
feat(ui): consolidate top sections into a single pill strip

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 8: Smoke verification

**Files:** none modified.

- [ ] **Step 8.1: Full Go suite**

```bash
cd /home/orenleiman/co/middleman && make test
```
Expected: PASS modulo known pre-existing failures (`TestAPIActivityReturnsUTCCreatedAt`, sandbox-only `TestWorkspaceDeleteDirty`). Anything else failing is a regression — investigate.

- [ ] **Step 8.2: Frontend tests + typechecks**

```bash
cd /home/orenleiman/co/middleman/frontend && bun run test
cd /home/orenleiman/co/middleman/packages/ui && bun run typecheck
cd /home/orenleiman/co/middleman/frontend && bun run typecheck
```
Expected: tests green; typechecks 0 errors.

- [ ] **Step 8.3: Manual UI verification**

Per CLAUDE.md "For UI or frontend changes, start the dev server and use the feature in a browser before reporting the task as complete."

In two terminals:

```bash
make dev
make frontend-dev
```

Walk through:

1. **Outer sidebar strip** — open the app, sidebar visible. Look for the new collapse chevron on the right edge. Click it → sidebar collapses. Click the existing collapse-button on the now-collapsed sidebar → sidebar expands again. Confirm.
2. **StackSidebar** — open a PR that belongs to a stack. Click the small chevron on the stack sidebar → it collapses to a vertical rail showing "Stack: <name> · N PRs". Click the rail → expands. Reload → state persists.
3. **DiffSidebar (review nav)** — open any PR's Review tab. Click the collapse chevron at the top of the review nav → collapses to a 30px rail showing `Nc · Nd · Nf`. Click the rail → expands. Drag the right-edge handle of the expanded review nav → width changes; reload → width persists.
4. **Per-section collapses** — toggle the chevrons on cover banner, commit message, patchset picker, brief card. All four should collapse/expand individually; reload → state persists for each.
5. **Consolidation** — click the ⋯ chip at the top-right of the section stack. The four banners disappear and the pill-strip appears. Pips reflect each section's underlying collapsed state (muted = was collapsed). Click a pip → that section expands inline beneath the strip. Click another pip → it switches. Click the same pip → it dismisses. Click the leading ‹ chevron on the strip → sections return to their pre-consolidation individual collapsed states.
6. **Regression sanity** — open the Activity tab on the same PR; confirm nothing's visually off. Switch to PR list; confirm nothing's broken there either.

If anything misbehaves, don't claim done. Investigate, fix in a focused commit on this branch, and re-run.

- [ ] **Step 8.4: Optional final commit** if any small adjustments were needed during 8.3.

---

## Spec coverage audit

Walking the spec section-by-section:

- Outer sidebar discoverability fix → Task 6.
- StackSidebar collapse-to-rail with persistence → Task 3.
- DiffSidebar collapse-to-rail with persistence → Task 4.
- DiffSidebar resize handle with width persistence → Task 5.
- Per-section collapse persistence:
  - `pr-cover-collapsed` already exists (no task).
  - `pr-commit-msg-collapsed` already exists (no task).
  - `pr-patchset-collapsed` → Task 1.
  - `pr-brief-collapsed` → Task 2.
- Consolidated strip + chip wiring → Task 7.
- Persistence keys table → covered across all tasks.
- Component touch list → matches the file map at the top of this plan.
- Manual smoke checklist → Task 8.

No gaps.

## Placeholder audit

Scanned for "TBD", "TODO", "implement later", "add validation", "etc". None present. A few tasks (Tasks 5 & 6) flag that the testing approach can be lighter ("the resize test can assert handle existence rather than full pointer simulation"; "the chevron test lives wherever is most natural") — these are deliberate-but-bounded latitude, not vague gaps. Every code-change step includes concrete code blocks.

## Type / name consistency

- `pr-patchset-collapsed`, `pr-brief-collapsed`, `pr-stack-sidebar-collapsed`, `pr-review-nav-collapsed`, `pr-review-nav-width`, `pr-top-sections-consolidated` — referenced consistently across the plan and the persistence schema table.
- `TopSectionsStrip` props: `pips`, `peeked`, `onPeek`, `onExpandAll` — defined in Task 7.3 and consumed by `PullDetail.svelte` in Task 7.5.
- `reviewNavCollapsed` state and `pr-ui-state` custom event — declared in Task 4.4, dispatched in Task 4.3, listened to in Task 4.4.
- Pip ids `cover` / `msg` / `patchset` / `brief` are stable across `TopSectionsStrip.svelte` and `PullDetail.svelte`.

No drift.
