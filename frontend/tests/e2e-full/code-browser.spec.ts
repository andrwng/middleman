import { expect, test } from "@playwright/test";
import { acquireExclusiveLock } from "./support/exclusiveLock";

// --- Code browser panel (git-backed, real diff pipeline) ---
// Uses the same real git repo as diff-view.spec.ts's and symbol-refs.spec.ts's
// git-backed suites -- testutil.SetupDiffRepo for acme/widgets PR #1 -- and
// the same exclusive lock ("git-backed-diff"), since all three specs drive
// the single shared e2e server against that one fixture. The diff contains:
//   - internal/handler.go: modified (2 hunks).
//   - internal/cache.go: added.
//   - config.yaml: deleted.
//   - README.md: whitespace-only change.
//
// internal/ is the fixture's one real subdirectory, holding two real files
// (handler.go, cache.go) -- exactly what's needed to exercise the tree pane's
// directory expansion.
//
// Commit-stepping caveat: ListCommits resolves to `git log mergeBase..head`,
// and this fixture's PR branch has exactly one commit on top of its base --
// so the sidebar/`[`/`]` commit-stepping surface only ever has ONE reachable
// commit (the head commit itself), and every DiffScope this repo can reach
// through real UI interaction (head, that one commit, or the "unreviewed"
// range collapsing to it) resolves to that same head SHA. There is no real,
// UI-reachable scope in this fixture whose current commit sha differs from
// head -- so the "missing at this commit" branch the design allows for
// cannot be exercised here without fabricating server responses. What IS
// real and worth asserting: stepping the scope (a real store transition,
// wired to a real keybinding) does not reset or clobber the panel's open
// file, which is the actual regression this design guards against.

test.describe("code browser panel (git-backed)", () => {
  test.describe.configure({ mode: "serial" });

  let releaseLock: (() => Promise<void>) | null = null;

  test.beforeAll(async () => {
    releaseLock = await acquireExclusiveLock("git-backed-diff");
  });

  test.afterAll(async () => {
    await releaseLock?.();
    releaseLock = null;
  });

  test.beforeEach(async ({ page }) => {
    await page.addInitScript(() => {
      localStorage.removeItem("diff-tab-width");
      localStorage.removeItem("diff-hide-whitespace");
      localStorage.removeItem("diff-collapsed-files");
      localStorage.removeItem("symbol-refs-gutter-width");
    });
  });

  // mode.serial (declared above) is what matters for these two tests, not
  // their relative order: both drive the code browser panel against the
  // SAME PR #1 fixture/server (and the same code-browser-state bookmark
  // row) under the shared "git-backed-diff" lock this file's siblings
  // (diff-view.spec.ts, symbol-refs.spec.ts) also use, so letting them run
  // concurrently would race that shared state. An earlier version of this
  // comment justified the ordering by a bookmark-priority bug -- open()
  // unconditionally preferring a stale server bookmark over a
  // deliberately-chosen path -- but that was fixed in a200db2 via the
  // forcePath mechanism (see OpenCodeBrowserOptions in
  // packages/ui/src/stores/codeBrowser.svelte.ts): browseToPath below
  // always opens with forcePath, so this test's seeding assertion holds
  // regardless of whatever bookmark a previous test left behind, and does
  // not depend on running before the toolbar test.
  test("the browse action on a symbol-refs hit opens the panel alongside the diff, seeded at that hit's file", async ({ page }) => {
    await page.goto("/pulls/acme/widgets/1/files");
    await page.locator(".diff-file").first().waitFor({ state: "visible", timeout: 10_000 });

    // No panel open yet.
    await expect(page.locator(".code-browser-panel")).toHaveCount(0);

    // Open a blank symbol search via the `s` key (same key path
    // symbol-refs.spec.ts uses) and search for "HandleRequest", which
    // occurs in internal/handler.go.
    await page.keyboard.press("s");
    const gutter = page.locator(".symref-gutter");
    const input = gutter.locator("[data-testid='symref-search']");
    await expect(input).toBeFocused();
    await input.fill("HandleRequest");
    await input.press("Enter");

    const row = gutter.locator(".symref-row").first();
    await expect(row).toBeVisible({ timeout: 10_000 });

    // Task 10's secondary browse action, distinct from the row's own
    // (jump-in-diff) click target.
    await gutter.getByLabel("Browse internal/handler.go in the code browser").click();

    const panel = page.locator(".code-browser-panel");
    await expect(panel).toBeVisible();
    await expect(panel.locator(".code-browser-title")).toHaveText("internal/handler.go");

    // Alongside, not instead of: both the gutter and the diff area are
    // still present behind the panel, and the gutter search itself is
    // untouched.
    await expect(gutter).toBeVisible();
    await expect(page.locator(".diff-area")).toBeVisible();
    await expect(input).toHaveValue("HandleRequest");
  });

  test("opens via the toolbar, browses into a subdirectory, opens a file, survives a commit-scope step, and resumes at the last path on reopen", async ({ page }) => {
    await page.goto("/pulls/acme/widgets/1/files");
    await page.locator(".diff-file").first().waitFor({ state: "visible", timeout: 10_000 });

    // Toolbar button: title carries the `(b)` shortcut hint, same
    // disambiguation pattern symbol-refs.spec.ts uses for its own
    // persistent-toolbar "Refs" button.
    await page.getByTitle("Browse files at this commit (b)").click();

    const panel = page.locator(".code-browser-panel");
    await expect(panel).toBeVisible();

    // The diff itself stays mounted behind the panel -- this is an overlay,
    // not a navigation away from the diff view.
    await expect(page.locator(".diff-area")).toBeVisible();

    // Expand the fixture's one real subdirectory and open one of its files.
    const internalDir = page.locator(".code-browser-entry--dir", { hasText: "internal" });
    await expect(internalDir).toBeVisible();
    await internalDir.click();

    const cacheFile = page.locator(".code-browser-entry--file", { hasText: "cache.go" });
    await expect(cacheFile).toBeVisible();
    await cacheFile.click();

    await expect(panel.locator(".code-browser-title")).toHaveText("internal/cache.go");
    const fileContent = panel.locator(".code-browser-file");
    await expect(fileContent).toBeVisible();

    // Shiki tokenizes asynchronously in batches; a tokenized span carries
    // the --lc/--dc dual-theme color custom properties CodeBrowserPanel
    // sets per span, while the untokenized fallback span carries neither.
    // Waiting on this (rather than a fixed sleep) rides out however long
    // tokenization takes, the same way symbol-refs.spec.ts's dblclickToken
    // waits on Shiki's spans before interacting with them.
    await expect(fileContent.locator("span[style*='--lc']").first())
      .toBeVisible({ timeout: 10_000 });
    await expect(fileContent).toContainText("NewCache");

    const openedPath = await panel.locator(".code-browser-title").textContent();
    expect(openedPath).toBe("internal/cache.go");

    // Step the diff scope with the real `]` keybinding (DiffView's own
    // handleKeydown, the same handler `b` and `s` go through). See the
    // top-of-file note: this fixture's single PR commit means the
    // underlying SHA does not change, but the scope object itself does
    // (head -> commit), which is the real transition the panel must
    // survive without resetting.
    await page.keyboard.press("]");
    await expect(panel.locator(".code-browser-title")).toHaveText(openedPath ?? "");
    await expect(fileContent).toContainText("NewCache");
    await expect(panel).toBeVisible();

    // Close via the panel's own close control (there is no Escape binding
    // for this panel in DiffView's keydown handler -- only the button).
    await panel.getByRole("button", { name: "Close" }).click();
    await expect(panel).not.toBeVisible();

    // Reopening resumes at the bookmarked path -- navigateTo() persisted it
    // server-side when cache.go was opened above, and open() reads it back.
    await page.getByTitle("Browse files at this commit (b)").click();
    await expect(page.locator(".code-browser-panel")).toBeVisible();
    await expect(page.locator(".code-browser-title")).toHaveText(openedPath ?? "");
    await expect(page.locator(".code-browser-file")).toContainText("NewCache");
  });
});
