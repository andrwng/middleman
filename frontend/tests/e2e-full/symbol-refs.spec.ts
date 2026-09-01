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

// installTokenSelection is dblclickToken's counterpart for the one
// selection a real double-click cannot make deterministic across
// browsers. WebKit's double-click word-expansion crosses a "." in a
// qualified call -- clicking inside "Println" in "fmt.Println" selects
// the whole "fmt.Println" -- while Chromium and Firefox stop at the
// "." and select just "Println", so the same double-click would search
// a different query per browser. This locates the exact token span the
// same way dblclickToken does, then builds a Range over its text node
// and installs it as the page's Selection directly via page.evaluate,
// bypassing native double-click word-boundary semantics entirely.
// DiffFile's selectionchange listener reacts to a script-driven
// selection change exactly as it would a user-driven one, so the Refs
// affordance appears the same way it does after a real double-click.
async function installTokenSelection(
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
  await token.waitFor({ state: "visible", timeout: 10_000 });
  const handle = await token.elementHandle();
  if (!handle) {
    throw new Error(`no token span with exact text "${exactSpanText}"`);
  }
  await fileLoc.page().evaluate((el) => {
    const textNode = el.firstChild;
    if (!textNode || textNode.nodeType !== Node.TEXT_NODE) {
      throw new Error("token span does not wrap a single text node");
    }
    const range = document.createRange();
    range.selectNodeContents(textNode);
    const sel = window.getSelection();
    if (!sel) throw new Error("window.getSelection() returned null");
    sel.removeAllRanges();
    sel.addRange(range);
  }, handle);
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

// readClassifier reports which labeller produced the currently displayed
// hits, purely from the DOM: SymbolRefsGutter renders the degraded note
// only when the store's classifier is "heuristic" (matched on the word
// "heuristic" rather than the note's exact wording, which could
// reasonably be reworded later). Callers must wait for the header count
// to settle to its expected value first -- classifier arrives in the
// same response as the count, so reading it any earlier risks the
// transient window where classifier is still "" and the note is absent
// regardless of what the eventual answer will be.
async function readClassifier(gutter: Locator): Promise<"heuristic" | "ctags"> {
  const note = gutter.locator(".symref-degraded-note");
  if ((await note.count()) === 0) return "ctags";
  await expect(note).toHaveText(/heuristic/);
  return "heuristic";
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
    await expect(gutter.locator(".symref-header__query")).toHaveValue("string");
    await expect(gutter.locator(".symref-header__count")).toHaveText("7", { timeout: 5000 });

    // Rows are grouped by file: both changed files that contain "string"
    // appear as group headers.
    const groupPaths = gutter.locator(".symref-group__path");
    await expect(groupPaths.filter({ hasText: "internal/cache.go" }).first()).toBeVisible();
    await expect(groupPaths.filter({ hasText: "internal/handler.go" }).first()).toBeVisible();

    // Definition-first ordering: the server sorts definition hits ahead
    // of plain references, so the very first row is a definition. The
    // kind class is classifier-agnostic and stays unconditional.
    const firstRow = gutter.locator(".symref-row").first();
    await expect(firstRow.locator(".symref-row__kind")).toHaveClass(/symref-row__kind--definition/);

    // The badge TEXT is classifier-dependent in principle: ctags
    // supplies it verbatim whenever a tag matches the hit. It reads
    // "def" under both classifiers here only because "string" is a Go
    // builtin type ctags never declares -- this exact hit stays
    // untagged even with ctags active, and falls back to the same
    // heuristic label, so the assertion below is unconditional.
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
    // scrolls to it and highlights it.
    const handlerGroup = gutter.locator(".symref-group", { hasText: "internal/handler.go" });
    await expect(handlerGroup.locator(".symref-row")).toHaveCount(1);
    await handlerGroup.locator(".symref-row").click();

    const target = page.locator(
      '.diff-file[data-file-path="internal/handler.go"] [data-anchor-line="34"][data-anchor-side="RIGHT"]',
    );
    await expect(target).toHaveClass(/line-wrap--jump-highlight/, { timeout: 5000 });
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
    await expect(gutter.locator(".symref-header__query")).toHaveValue("HandleRequest");
    await expect(gutter.locator(".symref-header__count")).toHaveText("2", { timeout: 5000 });

    // Only the definition renders as a main row up front.
    await expect(gutter.locator(".symref-row")).toHaveCount(1);
    const defRow = gutter.locator(".symref-row").first();

    // The badge TEXT is classifier-dependent: ctags tags HandleRequest
    // as a real Go "func" declaration (pinned precisely in the
    // dedicated ctags-kinds test below), so only the heuristic
    // classifier falls back to the coarse "def" label.
    const handleRequestClassifier = await readClassifier(gutter);
    if (handleRequestClassifier === "heuristic") {
      await expect(defRow.locator(".symref-row__kind")).toHaveText("def");
    } else {
      await expect(defRow.locator(".symref-row__kind")).toHaveText("func");
    }
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
    // Not dblclickToken: "Println" here is the tail of the qualified
    // call "fmt.Println", and WebKit's double-click word-expansion
    // crosses the "." to select "fmt.Println" while Chromium and
    // Firefox stop at the "." and select "Println" -- a different
    // query per browser. installTokenSelection selects exactly
    // "Println" programmatically so the query is deterministic.
    await installTokenSelection(handlerFile, 39, "RIGHT", "Println");

    const gutter = await openRefsPanel(page);
    await expect(gutter.locator(".symref-header__query")).toHaveValue("Println");
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

  test("clicking a row whose line sits in a collapsed context gap expands the region and lands on the highlighted target line", async ({ page }) => {
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
    await expect(gutter.locator(".symref-header__query")).toHaveValue("path");
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
    // to it, and it carries the same persistent highlight class an
    // already-rendered jump would.
    await target.waitFor({ state: "attached", timeout: 10_000 });
    await expect(target).toHaveClass(/line-wrap--jump-highlight/, { timeout: 5000 });
    await expect(target).toBeInViewport();

    // The gap shrank by exactly the 4 lines needed to reach line 18 from
    // its nearer (top) edge -- 17 - 4 = 13 -- rather than fully
    // expanding or expanding from the wrong side.
    await expect(collapsedBefore).toHaveCount(0);
    const collapsedAfter = handlerFile
      .locator(".collapsed-region")
      .filter({ hasText: "13 unchanged lines" });
    await expect(collapsedAfter).toHaveCount(1);

    // "path" also produces 2 string-classified hits alongside its 3
    // reference hits (Classify.go: the slog.Info call's "path" log key
    // at line 11, and the "empty path" literal at line 14, are both
    // still inside open quotes at the point "path" first appears on the
    // line). Neither test above ever opens the toggle on a "string"
    // kind -- test 1 and test 2 only ever expand a "comment" kind -- so
    // do that here, scoped via .symref-row__line rather than matching
    // on the row's full, whitespace-normalized text.
    const stringToggle = gutter.locator(".symref-toggle");
    await expect(stringToggle).toHaveText(/2 in comments\/strings/);
    await stringToggle.click();

    const line11Row = gutter.locator(".symref-row").filter({
      has: page.locator(".symref-row__line", { hasText: /^11$/ }),
    });
    await expect(line11Row).toHaveCount(1);
    await expect(line11Row.locator(".symref-row__kind")).toHaveText("string");
  });

  test("with ctags available, a Go function definition shows its precise kind while a call site keeps a plain reference", async ({ page }) => {
    await page.goto("/pulls/acme/widgets/1/files");
    await page.locator(".diff-file").first().waitFor({ state: "visible", timeout: 10_000 });

    const handlerFile = page.locator('.diff-file[data-file-path="internal/handler.go"]');

    // Same selection as the "HandleRequest" test above: a real Go func
    // declaration, which universal-ctags tags with kind "func".
    await dblclickToken(handlerFile, 10, "RIGHT", "HandleRequest");
    let gutter = await openRefsPanel(page);
    await expect(gutter.locator(".symref-header__count")).toHaveText("2", { timeout: 5000 });

    // Read the classifier from the page rather than assuming it: on a
    // machine without universal-ctags the server reports "heuristic",
    // and asserting a ctags-specific kind there would pass vacuously
    // (the label is never anything but the coarse fallback) while
    // proving nothing. Skip explicitly instead of tolerating both
    // outcomes with a lenient assertion.
    const classifier = await readClassifier(gutter);
    test.skip(classifier === "heuristic", "universal-ctags is not installed; kind labels stay heuristic");

    const defRow = gutter.locator(".symref-row").first();
    await expect(defRow.locator(".symref-row__kind")).toHaveClass(/symref-row__kind--definition/);
    await expect(defRow.locator(".symref-row__kind")).toHaveText("func");

    await gutter.locator(".symref-header__close").click();
    await expect(gutter).not.toBeAttached();

    // Same selection as the "Println" test above, and it uses
    // installTokenSelection for the same reason: "Println" is the tail
    // of a qualified "fmt.Println" call, and WebKit's double-click word
    // expansion crosses the "." and would search a different query.
    // The hit itself is a call site of a stdlib function this file never
    // declares, so ctags has no tag for it and it keeps the coarse
    // "reference" kind and "ref" label -- ctags only ever tags
    // declarations, never call sites.
    await installTokenSelection(handlerFile, 39, "RIGHT", "Println");
    gutter = await openRefsPanel(page);
    await expect(gutter.locator(".symref-header__count")).toHaveText("1", { timeout: 5000 });

    const refRow = gutter.locator(".symref-row").first();
    await expect(refRow.locator(".symref-row__kind")).toHaveClass(/symref-row__kind--reference/);
    await expect(refRow.locator(".symref-row__kind")).toHaveText("ref");
  });

  test("the toolbar button and the s key both open a focused search box, and a typed query returns rows without any selection", async ({ page }) => {
    await page.goto("/pulls/acme/widgets/1/files");
    await page.locator(".diff-file").first().waitFor({ state: "visible", timeout: 10_000 });

    const gutter = page.locator(".symref-gutter");
    await expect(gutter).toHaveCount(0);

    // The key path: no selection, no scrolling to an occurrence.
    await page.keyboard.press("s");
    await expect(gutter).toBeVisible({ timeout: 5000 });

    const input = gutter.locator("[data-testid='symref-search']");
    await expect(input).toBeFocused();
    await expect(gutter.locator(".symref-prompt")).toBeVisible();

    await input.fill("HandleRequest");
    await input.press("Enter");

    await expect(gutter.locator(".symref-row").first()).toBeVisible({ timeout: 10_000 });
    await expect(gutter.locator(".symref-prompt")).toHaveCount(0);

    // Closing and reopening via the toolbar button reaches the same state.
    await gutter.getByRole("button", { name: /close symbol references/i }).click();
    await expect(gutter).toHaveCount(0);

    // Not getByRole("button", { name: /^Refs$/ }): DiffFile's floating
    // selection-toolbar button (openRefsPanel's target above) carries the
    // identical visible text "Refs" and would make that locator match two
    // elements once something is selected. This is the persistent
    // DiffToolbar button, so it is located by its own title instead --
    // keeping both title-based makes the two "Refs" buttons easy to tell
    // apart.
    await page.getByTitle("Find references to a symbol (s)").click();
    await expect(gutter).toBeVisible({ timeout: 5000 });
    await expect(gutter.locator("[data-testid='symref-search']")).toBeFocused();
  });

  test("a multi-word query is refused with a reason rather than searching", async ({ page }) => {
    await page.goto("/pulls/acme/widgets/1/files");
    await page.locator(".diff-file").first().waitFor({ state: "visible", timeout: 10_000 });

    await page.keyboard.press("s");
    const gutter = page.locator(".symref-gutter");
    const input = gutter.locator("[data-testid='symref-search']");
    await input.fill("Handle Request");
    await input.press("Enter");

    await expect(gutter.locator(".symref-invalid")).toContainText(/whitespace/i);
    await expect(gutter.locator(".symref-row")).toHaveCount(0);
  });

  // Searching by highlighting a symbol records THAT symbol's line as the
  // departure point, not whatever line the viewport is centred on. The scroll
  // in the middle is what makes this test prove it: with the diff scrolled
  // away, the viewport's midline is nowhere near the selected symbol, so a
  // Back that returned to the viewport position could not land on line 13.
  test("Back returns to the symbol a search was launched from, wherever the diff has since been scrolled", async ({ page }) => {
    await page.goto("/pulls/acme/widgets/1/files");
    await page.locator(".diff-file").first().waitFor({ state: "visible", timeout: 10_000 });

    const handlerFile = page.locator('.diff-file[data-file-path="internal/handler.go"]');
    // `path` is declared on line 12 and tested on line 13; selecting on 13
    // keeps the launch point distinct from the row clicked below. The padded
    // token text matches how Shiki spans this identifier.
    await dblclickToken(handlerFile, 13, "RIGHT", " path ");
    const gutter = await openRefsPanel(page);

    const rows = gutter.locator(".symref-row");
    await expect(rows.first()).toBeVisible({ timeout: 10_000 });

    // Move the viewport far away from the selected symbol. Both lines 12 and
    // 13 sit near the top of the content, so scrolling to the end guarantees
    // the midline is in a different file entirely.
    await page.locator(".diff-area").evaluate((el) => {
      el.scrollTop = el.scrollHeight;
    });

    // Line 12's row: inside the already-rendered hunk, so the jump lands
    // immediately rather than arming a collapsed-region reveal that could
    // later move the highlight and race the assertion below.
    await rows.filter({ hasText: /^12\s/ }).first().click();
    await expect(page.locator(".line-wrap--jump-highlight")).toHaveCount(1);

    const back = gutter.getByRole("button", { name: /back to previous position/i });
    await expect(back).toBeEnabled();
    await back.click();

    // Back on the symbol that started the search -- same file, its own line.
    await expect(
      handlerFile.locator(".line-wrap--jump-highlight"),
    ).toHaveAttribute("data-anchor-line", "13");
    await expect(back).toBeDisabled();
  });

  test("Back walks the reader home through the positions each jump left, then disables itself", async ({ page }) => {
    await page.goto("/pulls/acme/widgets/1/files");
    await page.locator(".diff-file").first().waitFor({ state: "visible", timeout: 10_000 });

    await page.keyboard.press("s");
    const gutter = page.locator(".symref-gutter");
    const input = gutter.locator("[data-testid='symref-search']");
    // Not "HandleRequest": its only other occurrence is the doc comment
    // above it, which the comments/strings toggle collapses -- exactly
    // one visible row (the "doc-comment hit" test above asserts this
    // directly with toHaveCount(1)), too few to walk a trail through.
    // "path" has 3 visible rows -- handler.go lines 12, 13 and 18 (the
    // "empty path" and log-key hits at lines 11 and 14 are "string"
    // kind and collapse behind the toggle, as the "collapsed context
    // gap" test above establishes). Lines 12 and 13, clicked below,
    // both sit inside the hunk that is already rendered -- unlike line
    // 18, they need no collapsed-region expansion to land on.
    await input.fill("path");
    await input.press("Enter");

    const rows = gutter.locator(".symref-row");
    await expect(rows.first()).toBeVisible({ timeout: 10_000 });
    expect(await rows.count()).toBeGreaterThan(1);

    const back = gutter.getByRole("button", { name: /back to previous position/i });
    await expect(back).toBeDisabled();

    await rows.nth(0).click();
    const highlight = page.locator(".line-wrap--jump-highlight");
    await expect(highlight).toHaveCount(1);
    await expect(back).toBeEnabled();
    const firstLanding = await highlight.getAttribute("data-anchor-line");

    await rows.nth(1).click();
    const secondLanding = await highlight.getAttribute("data-anchor-line");
    expect(secondLanding).not.toBe(firstLanding);

    await back.click();
    // The round trip, and the assertion the original implementation failed:
    // a jump centres its target, so the position recorded when leaving the
    // first landing must BE the first landing. Asserting only "the highlight
    // moved off the second landing" passed happily while Back was returning to
    // a line half a viewport above where the reader had actually been.
    await expect.poll(async () => highlight.getAttribute("data-anchor-line"))
      .toBe(firstLanding);

    // One entry left, then the trail is exhausted.
    await expect(back).toBeEnabled();
    await back.click();
    await expect(back).toBeDisabled();
  });
});
