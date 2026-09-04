import { expect, test, type Page } from "@playwright/test";
import type { DiffResult, FilesResult } from "@middleman/ui/api/types";

// The sections above the diff (cover, commit message, AI summary, brief) each
// cap themselves, and the diff takes whatever is left -- it is the only child
// of the review-main column flexbox will shrink. Dragging a section's boundary
// hands the difference straight back to the diff, which is what these tests
// measure.

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
  const box = await page.locator(`[data-section-resize="${section}"]`).boundingBox();
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
  test("the message and the AI summary each get their own boundary", async ({ page }) => {
    await mockApi(page, { body: MESSAGE_BODY, analysis: SUMMARY });
    await openCommitScope(page, ".commit-banner__analysis-body");

    // Attached rather than visible: with both bodies at full height the column
    // over-subscribes and the lower boundary is clipped out of view.
    await expect(page.locator('[data-section-resize="commit-message"]')).toBeAttached();
    await expect(page.locator('[data-section-resize="commit-analysis"]')).toBeAttached();
  });

  test("shrinking the AI summary hands the height to the diff", async ({ page }) => {
    await mockApi(page, { analysis: SUMMARY });
    await openCommitScope(page, ".commit-banner__analysis-body");

    const summaryBefore = await heightOf(page, ".commit-banner__analysis-body");
    const diffBefore = await heightOf(page, ".diff-view");
    // The summary must actually overflow its cap for this to mean anything.
    expect(summaryBefore).toBeGreaterThan(200);

    await dragBoundary(page, "commit-analysis", -150);

    expect(await heightOf(page, ".commit-banner__analysis-body")).toBeCloseTo(summaryBefore - 150, 0);
    expect(await heightOf(page, ".diff-view")).toBeCloseTo(diffBefore + 150, 0);
  });

  test("shrinking the commit message hands the height to the diff", async ({ page }) => {
    await mockApi(page, { body: MESSAGE_BODY });
    await openCommitScope(page, ".commit-banner__body");

    const messageBefore = await heightOf(page, ".commit-banner__body");
    const diffBefore = await heightOf(page, ".diff-view");
    expect(messageBefore).toBeGreaterThan(150);

    await dragBoundary(page, "commit-message", -100);

    expect(await heightOf(page, ".commit-banner__body")).toBeCloseTo(messageBefore - 100, 0);
    expect(await heightOf(page, ".diff-view")).toBeCloseTo(diffBefore + 100, 0);
  });

  test("the two boundaries size their bodies independently", async ({ page }) => {
    await mockApi(page, { body: MESSAGE_BODY, analysis: SUMMARY });
    await openCommitScope(page, ".commit-banner__analysis-body");

    const summaryBefore = await heightOf(page, ".commit-banner__analysis-body");
    const messageBefore = await heightOf(page, ".commit-banner__body");

    await dragBoundary(page, "commit-message", -100);

    // Sizing the message leaves the summary's own cap untouched.
    expect(await heightOf(page, ".commit-banner__body")).toBeCloseTo(messageBefore - 100, 0);
    expect(await heightOf(page, ".commit-banner__analysis-body")).toBeCloseTo(summaryBefore, 0);
  });

  test("recovers a diff the sections had squeezed to nothing", async ({ page }) => {
    // An expanded message plus an AI summary claims 30vh + 50vh, which
    // over-subscribes the column: the diff is not merely thin, it is zero, and
    // `overflow: hidden` on the column clips the summary's boundary out of
    // reach. The topmost boundary stays reachable, and shrinking it pulls the
    // next one back into view -- that is the way out, in two drags.
    await mockApi(page, { body: MESSAGE_BODY, analysis: SUMMARY });
    await openCommitScope(page, ".commit-banner__analysis-body");

    expect(await heightOf(page, ".diff-view")).toBe(0);

    await dragBoundary(page, "commit-message", -500); // clamps to the floor
    await expect(page.locator('[data-section-resize="commit-analysis"]')).toBeVisible();

    await dragBoundary(page, "commit-analysis", -500);
    expect(await heightOf(page, ".diff-view")).toBeGreaterThan(100);
  });

  test("double-clicking a boundary gives the height back to the section", async ({ page }) => {
    await mockApi(page, { body: MESSAGE_BODY });
    await openCommitScope(page, ".commit-banner__body");

    const messageBefore = await heightOf(page, ".commit-banner__body");
    await dragBoundary(page, "commit-message", -100);
    expect(await heightOf(page, ".commit-banner__body")).toBeLessThan(messageBefore - 50);

    await page.locator('[data-section-resize="commit-message"]').dblclick();

    expect(await heightOf(page, ".commit-banner__body")).toBeCloseTo(messageBefore, 0);
  });
});
