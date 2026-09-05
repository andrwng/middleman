import { expect, test, type Page } from "@playwright/test";
import type { DiffResult, FilesResult } from "@middleman/ui/api/types";

// Two levels of sizing above the diff. The top block as a whole is bounded and
// scrollable, and one boundary trades its height against the diff. Inside it,
// each section body (cover, commit message, AI summary, brief) has its own
// boundary deciding how much of the block it takes.
//
// The bound is what makes any of it usable: unbounded, the block overflowed the
// column, `.review-main`'s `overflow: hidden` clipped the excess with no
// scrollbar, and the lower sections' boundaries went out of reach with it -- the
// AI summary's handle sat 119px below the bottom of the column. The first test
// here is that regression.

const SHA = "abc1234def5678901234567890123456789012ab";

// Long enough that both bodies overflow their caps (30vh for the message,
// 50vh for the summary on a 720px viewport). Without that a max-height change
// could not move the rendered box and the assertions would be vacuous.
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
  // A commit body and an AI summary cap at 30vh and 50vh. Together they
  // over-subscribe the column, so tests that need a 1:1 handoff to the diff
  // ask for one at a time.
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

async function dragBoundary(page: Page, section: string, dy: number): Promise<void> {
  const handle = page.locator(`[data-section-resize="${section}"]`);
  // The top block scrolls, so a boundary inside it may be out of view -- the
  // reader scrolls to it before dragging, and so does this.
  await handle.scrollIntoViewIfNeeded();
  const box = await handle.boundingBox();
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

// Returns the handle's background after hovering it, to check the drag
// affordance appears. This says nothing about reachability: hover auto-scrolls,
// and an ancestor with `overflow: hidden` still scrolls programmatically, so
// Playwright can reach a handle a reader cannot. See the first test.
async function hoverBackground(page: Page, section: string): Promise<string> {
  const handle = page.locator(`[data-section-resize="${section}"]`);
  await handle.hover();
  return await handle.evaluate((el) => getComputedStyle(el).backgroundColor);
}

test.describe("review section resize", () => {
  test("the AI summary's boundary is reachable by scrolling, and lights up", async ({ page }) => {
    await mockApi(page, { body: MESSAGE_BODY, analysis: SUMMARY });
    await openCommitScope(page, ".commit-banner__analysis-body");

    const handle = page.locator('[data-section-resize="commit-analysis"]');

    // Scrolling the top block is the only move a reader has. Unbounded, the
    // block was not a scroll container at all: the column's `overflow: hidden`
    // clipped this handle 119px past its bottom edge, where nothing a reader
    // does could bring it.
    //
    // Note hover() alone cannot express that. `overflow: hidden` still scrolls
    // PROGRAMMATICALLY, so Playwright's auto-scroll reaches a handle no human
    // can -- a hover assertion here passes with the bug in place. The reader's
    // own scroll is the honest test.
    await page.locator(".top-sections").evaluate((el) => {
      el.scrollTop = el.scrollHeight;
    });
    await expect(handle).toBeInViewport();

    // And once reached it shows the drag affordance: transparent until hover.
    expect(await handle.evaluate((el) => getComputedStyle(el).backgroundColor))
      .toBe("rgba(0, 0, 0, 0)");
    expect(await hoverBackground(page, "commit-analysis")).not.toBe("rgba(0, 0, 0, 0)");
    expect(await hoverBackground(page, "top-sections")).not.toBe("rgba(0, 0, 0, 0)");
  });

  test("the diff keeps a readable share however tall the sections are", async ({ page }) => {
    await mockApi(page, { body: MESSAGE_BODY, analysis: SUMMARY });
    await openCommitScope(page, ".commit-banner__analysis-body");

    // The message and the summary together want 80vh. Before the block was
    // bounded this left the diff at exactly zero pixels.
    expect(await heightOf(page, ".diff-view")).toBeGreaterThan(200);
  });

  test("dragging the review-sections boundary trades height with the diff", async ({ page }) => {
    await mockApi(page, { body: MESSAGE_BODY, analysis: SUMMARY });
    await openCommitScope(page, ".commit-banner__analysis-body");

    const topBefore = await heightOf(page, ".top-sections");
    const diffBefore = await heightOf(page, ".diff-view");

    await dragBoundary(page, "top-sections", -120);

    expect(await heightOf(page, ".top-sections")).toBeCloseTo(topBefore - 120, 0);
    expect(await heightOf(page, ".diff-view")).toBeCloseTo(diffBefore + 120, 0);
  });

  test("the review-sections boundary gives the height back on double-click", async ({ page }) => {
    await mockApi(page, { body: MESSAGE_BODY, analysis: SUMMARY });
    await openCommitScope(page, ".commit-banner__analysis-body");

    const topBefore = await heightOf(page, ".top-sections");
    await dragBoundary(page, "top-sections", -120);
    expect(await heightOf(page, ".top-sections")).toBeLessThan(topBefore - 100);

    await page.locator('[data-section-resize="top-sections"]').dblclick();

    expect(await heightOf(page, ".top-sections")).toBeCloseTo(topBefore, 0);
  });

  test("a section boundary sizes its own body inside the block", async ({ page }) => {
    await mockApi(page, { body: MESSAGE_BODY, analysis: SUMMARY });
    await openCommitScope(page, ".commit-banner__analysis-body");

    const messageBefore = await heightOf(page, ".commit-banner__body");
    expect(messageBefore).toBeGreaterThan(150);

    await dragBoundary(page, "commit-message", -100);

    expect(await heightOf(page, ".commit-banner__body")).toBeCloseTo(messageBefore - 100, 0);
  });

  test("the two section boundaries size their bodies independently", async ({ page }) => {
    await mockApi(page, { body: MESSAGE_BODY, analysis: SUMMARY });
    await openCommitScope(page, ".commit-banner__analysis-body");

    const summaryBefore = await heightOf(page, ".commit-banner__analysis-body");
    const messageBefore = await heightOf(page, ".commit-banner__body");

    await dragBoundary(page, "commit-message", -100);

    expect(await heightOf(page, ".commit-banner__body")).toBeCloseTo(messageBefore - 100, 0);
    expect(await heightOf(page, ".commit-banner__analysis-body")).toBeCloseTo(summaryBefore, 0);
  });

  test("a short stack leaves its slack to the diff rather than reserving it", async ({ page }) => {
    await mockApi(page, { body: "one short line" });
    await openCommitScope(page, ".commit-banner__body");

    const top = await heightOf(page, ".top-sections");
    const diff = await heightOf(page, ".diff-view");
    // The bound is a max-height, so a small stack takes only what it needs.
    expect(top).toBeLessThan(300);
    expect(diff).toBeGreaterThan(top);
  });

  test("double-clicking a section boundary gives the height back to the body", async ({ page }) => {
    await mockApi(page, { body: MESSAGE_BODY });
    await openCommitScope(page, ".commit-banner__body");

    const messageBefore = await heightOf(page, ".commit-banner__body");
    await dragBoundary(page, "commit-message", -100);
    expect(await heightOf(page, ".commit-banner__body")).toBeLessThan(messageBefore - 50);

    await page.locator('[data-section-resize="commit-message"]').dblclick();

    expect(await heightOf(page, ".commit-banner__body")).toBeCloseTo(messageBefore, 0);
  });
});
