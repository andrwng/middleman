import { expect, test, type Page } from "@playwright/test";
import type { DiffResult, FilesResult } from "@middleman/ui/api/types";

// One boundary above the diff sizes the whole block of sections that sits there
// -- cover, commit message, AI summary, brief. The block is bounded and
// scrolls; the diff takes the rest.
//
// The bound is the load-bearing part. Unbounded, the block grew past the column
// and `.review-main`'s `overflow: hidden` clipped the excess with no scrollbar:
// an expanded commit message plus an AI summary left the diff at exactly zero
// pixels, with the foot of the block unreachable. Two tests here pin that.

const SHA = "abc1234def5678901234567890123456789012ab";

// Long enough that the block overflows the column, which is the situation the
// bound exists for. Without that these tests would prove nothing.
const MESSAGE_BODY = Array.from(
  { length: 40 },
  (_, i) => `explanatory paragraph ${i} of the commit message`,
).join("\n");

const SUMMARY = Array.from(
  { length: 60 },
  (_, i) => `- summary bullet ${i} about what this commit does`,
).join("\n");

const diff: DiffResult = {
  stale: false,
  whitespace_only_count: 0,
  files: [
    {
      path: "internal/server/handler.go",
      old_path: "internal/server/handler.go",
      status: "modified",
      is_binary: false,
      is_whitespace_only: false,
      additions: 1,
      deletions: 0,
      hunks: [
        {
          old_start: 1,
          old_count: 1,
          new_start: 1,
          new_count: 2,
          section: "",
          lines: [
            { type: "context", content: "package server", old_num: 1, new_num: 1 },
            { type: "add", content: "// touched", new_num: 2 },
          ],
        },
      ],
    },
  ],
};

function filesFromDiff(fixture: DiffResult): FilesResult {
  return {
    stale: fixture.stale,
    files: fixture.files.map((f) => ({ ...f, additions: 0, deletions: 0, hunks: [] })),
  };
}

interface Content {
  body?: string;
  analysis?: string;
}

async function mockApi(page: Page, content: Content): Promise<void> {
  await page.route("**/api/v1/repos/acme/widgets/pulls/1/files", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(filesFromDiff(diff)),
    });
  });
  await page.route("**/api/v1/repos/acme/widgets/pulls/1/diff*", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(diff),
    });
  });
  await page.route("**/api/v1/repos/acme/widgets/pulls/*/commits", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        commits: [
          {
            sha: SHA,
            message: "feat: do the thing",
            body: content.body ?? "",
            authored_at: "2026-04-01T00:00:00Z",
            author_name: "alice",
          },
        ],
      }),
    });
  });
  await page.route("**/api/v1/repos/acme/widgets/pulls/*/commits/*/analyze", async (route) => {
    if (content.analysis === undefined) {
      // 404 is how the store learns a commit has no analysis yet.
      await route.fulfill({ status: 404, contentType: "application/json", body: "{}" });
      return;
    }
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        id: 1,
        mr_id: 1,
        commit_sha: SHA,
        status: "done",
        content: content.analysis,
        error: "",
        created_at: "2026-04-01T00:00:00Z",
        started_at: "2026-04-01T00:00:00Z",
        completed_at: "2026-04-01T00:00:01Z",
      }),
    });
  });
}

// The commit-message banner renders only while the diff scope is a single
// commit, so the fixture gets there the way a reader does: by clicking the
// commit in the review nav.
async function openCommitScope(page: Page, settled: string): Promise<void> {
  await page.goto("/pulls/acme/widgets/1/files");
  await page.locator(".commit-item").first().click();
  await page.locator(settled).waitFor({ state: "visible", timeout: 10_000 });
}

const DIVIDER = '[data-section-resize="top-sections"]';

async function dragDivider(page: Page, dy: number): Promise<void> {
  const box = await page.locator(DIVIDER).boundingBox();
  expect(box).not.toBeNull();
  const x = box!.x + box!.width / 2;
  const y = box!.y + box!.height / 2;
  await page.mouse.move(x, y);
  await page.mouse.down();
  await page.mouse.move(x, y + dy, { steps: 8 });
  await page.mouse.up();
}

async function heightOf(page: Page, selector: string): Promise<number> {
  const box = await page.locator(selector).boundingBox();
  expect(box).not.toBeNull();
  return box!.height;
}

test.describe("review section resize", () => {
  test("the diff keeps a readable share however tall the frontmatter is", async ({ page }) => {
    await mockApi(page, { body: MESSAGE_BODY, analysis: SUMMARY });
    await openCommitScope(page, ".commit-banner__analysis-body");

    // The message and the summary together want 80vh. Unbounded, that left the
    // diff at exactly zero pixels.
    expect(await heightOf(page, ".diff-view")).toBeGreaterThan(200);
  });

  test("the frontmatter scrolls what will not fit instead of clipping it", async ({ page }) => {
    await mockApi(page, { body: MESSAGE_BODY, analysis: SUMMARY });
    await openCommitScope(page, ".commit-banner__analysis-body");

    // Unbounded, `.top-sections` was not a scroll container at all: whatever
    // passed the column's edge was clipped by `overflow: hidden` and no reader
    // action could bring it back.
    //
    // This has to be checked by scrolling it ourselves. `overflow: hidden`
    // still scrolls PROGRAMMATICALLY, so Playwright's own auto-scroll (inside
    // hover, click, scrollIntoViewIfNeeded) reaches content a reader cannot --
    // assertions built on those pass with the bug in place.
    const scrolled = await page.locator(".top-sections").evaluate((el) => {
      const overflows = el.scrollHeight > el.clientHeight;
      el.scrollTop = el.scrollHeight;
      return { overflows, scrollTop: el.scrollTop };
    });
    expect(scrolled.overflows).toBe(true);
    expect(scrolled.scrollTop).toBeGreaterThan(0);
    // The foot of the block is on screen now, not past the column's edge.
    await expect(page.locator(".commit-banner__analysis")).toBeInViewport();
  });

  test("the divider is reachable with everything open, and lights up", async ({ page }) => {
    await mockApi(page, { body: MESSAGE_BODY, analysis: SUMMARY });
    await openCommitScope(page, ".commit-banner__analysis-body");

    const divider = page.locator(DIVIDER);
    // It sits outside the scrolling block, so no amount of frontmatter can
    // push it out of reach.
    await expect(divider).toBeInViewport();
    expect(await divider.evaluate((el) => getComputedStyle(el).backgroundColor))
      .toBe("rgba(0, 0, 0, 0)");
    await divider.hover();
    expect(await divider.evaluate((el) => getComputedStyle(el).backgroundColor))
      .not.toBe("rgba(0, 0, 0, 0)");
  });

  test("dragging the divider trades height with the diff", async ({ page }) => {
    await mockApi(page, { body: MESSAGE_BODY, analysis: SUMMARY });
    await openCommitScope(page, ".commit-banner__analysis-body");

    const topBefore = await heightOf(page, ".top-sections");
    const diffBefore = await heightOf(page, ".diff-view");

    await dragDivider(page, -120);

    expect(await heightOf(page, ".top-sections")).toBeCloseTo(topBefore - 120, 0);
    expect(await heightOf(page, ".diff-view")).toBeCloseTo(diffBefore + 120, 0);

    // And back the other way, from the divider's new position.
    await dragDivider(page, 60);
    expect(await heightOf(page, ".top-sections")).toBeCloseTo(topBefore - 60, 0);
    expect(await heightOf(page, ".diff-view")).toBeCloseTo(diffBefore + 60, 0);
  });

  test("the chosen split survives a reload", async ({ page }) => {
    await mockApi(page, { body: MESSAGE_BODY, analysis: SUMMARY });
    await openCommitScope(page, ".commit-banner__analysis-body");

    const topBefore = await heightOf(page, ".top-sections");
    await dragDivider(page, -120);
    const chosen = Math.round(topBefore - 120);

    await page.reload();
    await page.locator(".diff-view").waitFor({ state: "visible", timeout: 10_000 });

    // A reload drops back to the head scope, so the block holds less and its
    // rendered height need not match. The cap it was given is what persists.
    expect(await page.locator(".top-sections").evaluate((el) => el.style.maxHeight))
      .toBe(`${chosen}px`);
  });

  test("the divider gives the height back on double-click", async ({ page }) => {
    await mockApi(page, { body: MESSAGE_BODY, analysis: SUMMARY });
    await openCommitScope(page, ".commit-banner__analysis-body");

    const topBefore = await heightOf(page, ".top-sections");
    await dragDivider(page, -120);
    expect(await heightOf(page, ".top-sections")).toBeLessThan(topBefore - 100);

    await page.locator(DIVIDER).dblclick();

    expect(await heightOf(page, ".top-sections")).toBeCloseTo(topBefore, 0);
    expect(await page.locator(".top-sections").evaluate((el) => el.style.maxHeight)).toBe("");
  });

  test("a short frontmatter leaves its slack to the diff rather than reserving it", async ({ page }) => {
    await mockApi(page, { body: "one short line" });
    await openCommitScope(page, ".commit-banner__body");

    const top = await heightOf(page, ".top-sections");
    const diff = await heightOf(page, ".diff-view");
    // The bound is a max-height, so a small stack takes only what it needs.
    expect(top).toBeLessThan(300);
    expect(diff).toBeGreaterThan(top);
  });
});
