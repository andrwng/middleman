import { expect, test, type Locator, type Page } from "@playwright/test";
import { acquireExclusiveLock } from "./support/exclusiveLock";

// --- Symbol references gutter (git-backed, real diff pipeline) ---
// Uses the same real git repo as diff-view.spec.ts's git-backed suite --
// testutil.SetupDiffRepo for acme/widgets PR #1 -- and the same exclusive
// lock, since both specs drive the single shared e2e server against that
// one fixture. The diff contains:
//   - internal/handler.go: modified (2 hunks: log->slog in HandleRequest,
//     an added fmt.Println in ProcessEvent), with a 17-line collapsed gap
//     between the hunks (old/new lines 15-31) containing the rest of
//     HandleRequest's body plus the "// line 1".."// line 10" spacing
//     comments.
//   - internal/cache.go: added (a single hunk covering the whole file).
//   - config.yaml: deleted.
//   - README.md: whitespace-only change.
//
// Symbol selection is driven with real double-clicks on syntax-highlighted
// tokens (Shiki gives each identifier its own <span> once tokenization
// completes), not a synthesized Selection -- this is the only place in the
// suite that exercises DiffFile's real selectionchange listener and the
// DiffView layout Task 9 introduced (a flex row of .diff-area plus this
// gutter column).

async function dblclickToken(
  fileLoc: Locator,
  line: number,
  side: "LEFT" | "RIGHT",
  exactSpanText: string,
): Promise<void> {
  await fileLoc.scrollIntoViewIfNeeded();
  const wrap = fileLoc.locator(
    `.line-wrap[data-anchor-line="${line}"][data-anchor-side="${side}"]`,
  );
  await wrap.scrollIntoViewIfNeeded();
  const escaped = exactSpanText.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const token = wrap
    .locator(".code span")
    .filter({ hasText: new RegExp(`^${escaped}$`) })
    .first();
  // Shiki tokenizes asynchronously and only once the file is scrolled
  // into view; waiting here (rather than a fixed sleep) rides that out
  // regardless of how long it takes.
  await token.waitFor({ state: "visible", timeout: 10_000 });
  await token.dblclick();
}

// openRefsPanel clicks the floating toolbar's Refs button (which only
// the preceding double-click makes visible) and returns the gutter
// locator once it is attached and visible.
async function openRefsPanel(page: Page): Promise<Locator> {
  const refsBtn = page.getByTitle("Find other references to this symbol");
  await expect(refsBtn).toBeVisible({ timeout: 5000 });
  await refsBtn.click();
  const gutter = page.locator(".symref-gutter");
  await expect(gutter).toBeVisible({ timeout: 5000 });
  return gutter;
}

test.describe("symbol references gutter (git-backed)", () => {
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

  test("selecting a symbol in two changed files groups the gutter by file, collapses comments/strings, jumps to an in-hunk hit, and restores full width on close", async ({ page }) => {
    await page.goto("/pulls/acme/widgets/1/files");
    await page.locator(".diff-file").first().waitFor({ state: "visible", timeout: 10_000 });

    // "string" appears (as a Go type) in both internal/cache.go and
    // internal/handler.go, and also inside a cache.go doc comment --
    // exactly the shape needed for cross-file grouping plus the
    // comments/strings expander.
    const cacheFile = page.locator('.diff-file[data-file-path="internal/cache.go"]');
    await dblclickToken(cacheFile, 30, "RIGHT", "string");

    const gutter = await openRefsPanel(page);
    await expect(gutter.locator(".symref-header__query")).toHaveText("string");
    await expect(gutter.locator(".symref-header__count")).toHaveText("7", { timeout: 5000 });

    // Rows are grouped by file: both changed files that contain "string"
    // appear as group headers.
    const groupPaths = gutter.locator(".symref-group__path");
    await expect(groupPaths.filter({ hasText: "internal/cache.go" }).first()).toBeVisible();
    await expect(groupPaths.filter({ hasText: "internal/handler.go" }).first()).toBeVisible();

    // Definition-first ordering: the server sorts definition hits ahead
    // of plain references, so the very first row is a definition.
    const firstRow = gutter.locator(".symref-row").first();
    await expect(firstRow.locator(".symref-row__kind")).toHaveClass(/symref-row__kind--definition/);
    await expect(firstRow.locator(".symref-row__kind")).toHaveText("def");

    // The one comment hit (cache.go's "Returns empty string if" doc
    // comment) starts collapsed behind the toggle, not as a visible row.
    const commentRow = gutter.locator(".symref-row", { hasText: "Returns empty string if" });
    await expect(commentRow).toHaveCount(0);
    const toggle = gutter.locator(".symref-toggle");
    await expect(toggle).toHaveText(/1 in comments\/strings/);
    await toggle.click();
    await expect(commentRow).toBeVisible();
    await expect(commentRow.locator(".symref-row__kind")).toHaveText("comment");

    // No repo-wide footer here: "string" does not occur outside the
    // PR's changed files in this fixture (that case is covered below).
    await expect(gutter.locator(".symref-footer")).toHaveCount(0);

    // Layout: Task 9 restructured DiffView into a flex row of .diff-area
    // plus this gutter column. The diff pane must stay usably wide and
    // the gutter must be fully on screen, not pushed off the viewport.
    const diffArea = page.locator(".diff-area");
    const diffBoxOpen = await diffArea.boundingBox();
    expect(diffBoxOpen).not.toBeNull();
    expect(diffBoxOpen!.width).toBeGreaterThan(200);
    await expect(page.locator(".symref-gutter-resize")).toBeVisible();
    await expect(gutter).toBeInViewport({ ratio: 1 });

    // Click a row inside a rendered hunk -- handler.go's ProcessEvent
    // signature sits well within the file's second hunk -- and the diff
    // scrolls to it and flashes it.
    const handlerGroup = gutter.locator(".symref-group", { hasText: "internal/handler.go" });
    await expect(handlerGroup.locator(".symref-row")).toHaveCount(1);
    await handlerGroup.locator(".symref-row").click();

    const target = page.locator(
      '.diff-file[data-file-path="internal/handler.go"] [data-anchor-line="34"][data-anchor-side="RIGHT"]',
    );
    await expect(target).toHaveClass(/line-wrap--flash/, { timeout: 5000 });
    await expect(target).toBeInViewport();

    // Closing the gutter returns the diff to full width.
    await gutter.locator(".symref-header__close").click();
    await expect(gutter).not.toBeAttached();
    await expect(page.locator(".symref-gutter-resize")).not.toBeAttached();
    const diffBoxClosed = await diffArea.boundingBox();
    expect(diffBoxClosed).not.toBeNull();
    expect(diffBoxClosed!.width).toBeGreaterThan(diffBoxOpen!.width + 100);
  });

  test("a symbol with a doc-comment hit sorts its definition first and keeps the comment collapsed until expanded", async ({ page }) => {
    await page.goto("/pulls/acme/widgets/1/files");
    await page.locator(".diff-file").first().waitFor({ state: "visible", timeout: 10_000 });

    // "HandleRequest" appears exactly twice in the changed files: the
    // func declaration (a definition) and the doc comment above it
    // (a comment) -- nowhere else in the fixture's tree.
    const handlerFile = page.locator('.diff-file[data-file-path="internal/handler.go"]');
    await dblclickToken(handlerFile, 10, "RIGHT", "HandleRequest");

    const gutter = await openRefsPanel(page);
    await expect(gutter.locator(".symref-header__query")).toHaveText("HandleRequest");
    await expect(gutter.locator(".symref-header__count")).toHaveText("2", { timeout: 5000 });

    // Only the definition renders as a main row up front.
    await expect(gutter.locator(".symref-row")).toHaveCount(1);
    const defRow = gutter.locator(".symref-row").first();
    await expect(defRow.locator(".symref-row__kind")).toHaveText("def");
    await expect(defRow.locator(".symref-row__line")).toHaveText("10");

    const commentRow = gutter.locator(".symref-row", { hasText: "processes incoming HTTP requests" });
    await expect(commentRow).toHaveCount(0);
    await expect(gutter.locator(".symref-toggle")).toHaveText(/1 in comments\/strings/);

    await gutter.locator(".symref-toggle").click();
    await expect(gutter.locator(".symref-row")).toHaveCount(2);
    await expect(commentRow).toBeVisible();
    await expect(commentRow.locator(".symref-row__kind")).toHaveText("comment");
    await expect(commentRow.locator(".symref-row__line")).toHaveText("9");
  });

  test("a symbol with hits both inside and outside the PR's changed files shows a non-interactive repo-wide count", async ({ page }) => {
    await page.goto("/pulls/acme/widgets/1/files");
    await page.locator(".diff-file").first().waitFor({ state: "visible", timeout: 10_000 });

    // "Println" appears once in the PR (the added line in ProcessEvent)
    // and once outside it (main.go's unchanged fmt.Println), which is
    // exactly what makes one hit listed and one counted as outside.
    const handlerFile = page.locator('.diff-file[data-file-path="internal/handler.go"]');
    await dblclickToken(handlerFile, 39, "RIGHT", "Println");

    const gutter = await openRefsPanel(page);
    await expect(gutter.locator(".symref-header__query")).toHaveText("Println");
    await expect(gutter.locator(".symref-header__count")).toHaveText("1", { timeout: 5000 });
    await expect(gutter.locator(".symref-row")).toHaveCount(1);
    await expect(gutter.locator(".symref-row__line")).toHaveText("39");

    const footer = gutter.locator(".symref-footer");
    await expect(footer).toHaveText("+1 elsewhere in the repo");
    // A plain, non-interactive row -- not a button or a link.
    const tagName = await footer.evaluate((el) => el.tagName);
    expect(tagName).toBe("DIV");
    const role = await footer.getAttribute("role");
    expect(role).toBeNull();
  });

  test("clicking a row whose line sits in a collapsed context gap expands the region and lands on the flashed target line", async ({ page }) => {
    await page.goto("/pulls/acme/widgets/1/files");
    await page.locator(".diff-file").first().waitFor({ state: "visible", timeout: 10_000 });

    const handlerFile = page.locator('.diff-file[data-file-path="internal/handler.go"]');
    await handlerFile.scrollIntoViewIfNeeded();

    // Sanity: the collapsed region between the two hunks (17 unchanged
    // lines: old/new 15..31) has not been touched yet.
    const collapsedBefore = handlerFile
      .locator(".collapsed-region")
      .filter({ hasText: "17 unchanged lines" });
    await expect(collapsedBefore).toHaveCount(1);

    // Select "path" from line 13, which sits inside the first (already
    // rendered) hunk -- so no expansion happens just to make the
    // selection possible.
    await dblclickToken(handlerFile, 13, "RIGHT", " path ");

    const selectedText = await page.evaluate(() => window.getSelection()?.toString() ?? "");
    expect(selectedText).toBe("path");

    const gutter = await openRefsPanel(page);
    await expect(gutter.locator(".symref-header__query")).toHaveText("path");
    await expect(gutter.locator(".symref-header__count")).toHaveText("5", { timeout: 5000 });

    // Honesty check: line 18 -- the fmt.Fprintf(w, "OK: %s", path) call,
    // which falls in the collapsed gap -- genuinely is not in the DOM
    // yet. Everything below only means something because this is true.
    const target = page.locator(
      '.diff-file[data-file-path="internal/handler.go"] [data-anchor-line="18"][data-anchor-side="RIGHT"]',
    );
    await expect(target).toHaveCount(0);

    const row18 = gutter.locator(".symref-row", { hasText: /^18\s/ });
    await expect(row18).toHaveCount(1);
    await row18.click();

    // The region expands to bring line 18 into the DOM, the diff scrolls
    // to it, and it carries the same flash class an already-rendered
    // jump would.
    await target.waitFor({ state: "attached", timeout: 10_000 });
    await expect(target).toHaveClass(/line-wrap--flash/, { timeout: 5000 });
    await expect(target).toBeInViewport();

    // The gap shrank by exactly the 4 lines needed to reach line 18 from
    // its nearer (top) edge -- 17 - 4 = 13 -- rather than fully
    // expanding or expanding from the wrong side.
    await expect(collapsedBefore).toHaveCount(0);
    const collapsedAfter = handlerFile
      .locator(".collapsed-region")
      .filter({ hasText: "13 unchanged lines" });
    await expect(collapsedAfter).toHaveCount(1);
  });
});
