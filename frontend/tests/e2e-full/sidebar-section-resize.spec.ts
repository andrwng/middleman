import { expect, test, type Page } from "@playwright/test";
import type { DiffResult, FilesResult } from "@middleman/ui/api/types";

// Dragging the boundary at the bottom of a sidebar section sizes that section.
// The cap is a max-height, so shrinking is what shows up in the layout: these
// tests give the commit list more commits than its default 40vh cap can show,
// then drag the boundary upward and watch the rendered body follow.

// 24 rows comfortably exceeds 40vh of a 720px-tall viewport.
const COMMIT_COUNT = 24;

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

function commitPayload(): string {
  const commits = Array.from({ length: COMMIT_COUNT }, (_, i) => ({
    sha: `${i}`.padStart(2, "0").repeat(20),
    message: `commit number ${i}`,
    authored_at: "2026-04-01T00:00:00Z",
    author_name: "alice",
  }));
  return JSON.stringify({ commits });
}

async function mockApi(page: Page): Promise<void> {
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
    await route.fulfill({ status: 200, contentType: "application/json", body: commitPayload() });
  });
}

async function openReview(page: Page): Promise<void> {
  await page.goto("/pulls/acme/widgets/1/files");
  await page.locator(".commit-item").first().waitFor({ state: "visible", timeout: 10_000 });
}

// Drags the boundary by `dy` pixels (negative shrinks the section above it).
async function dragBoundary(page: Page, section: string, dy: number): Promise<void> {
  const handle = page.locator(`[data-section-resize="${section}"]`);
  const box = await handle.boundingBox();
  expect(box).not.toBeNull();
  const x = box!.x + box!.width / 2;
  const y = box!.y + box!.height / 2;
  await page.mouse.move(x, y);
  await page.mouse.down();
  await page.mouse.move(x, y + dy, { steps: 8 });
  await page.mouse.up();
}

async function bodyHeight(page: Page): Promise<number> {
  const box = await page.locator(".commit-section__body").boundingBox();
  expect(box).not.toBeNull();
  return box!.height;
}

test.describe("sidebar section resize", () => {
  test("dragging the boundary up shrinks the section above it", async ({ page }) => {
    await mockApi(page);
    await openReview(page);

    const before = await bodyHeight(page);
    // The fixture must actually overflow the default cap, or a max-height
    // change could not move the rendered box and this test would be vacuous.
    expect(before).toBeGreaterThan(200);

    await dragBoundary(page, "commits", -150);

    const after = await bodyHeight(page);
    expect(after).toBeCloseTo(before - 150, 0);
    expect(await page.locator(".commit-section__body").evaluate((el) => el.style.maxHeight))
      .toBe(`${Math.round(before - 150)}px`);
  });

  test("the chosen height survives a reload", async ({ page }) => {
    await mockApi(page);
    await openReview(page);

    const before = await bodyHeight(page);
    await dragBoundary(page, "commits", -150);
    const resized = await bodyHeight(page);
    expect(resized).toBeLessThan(before - 100);

    await page.reload();
    await page.locator(".commit-item").first().waitFor({ state: "visible", timeout: 10_000 });

    expect(await bodyHeight(page)).toBeCloseTo(resized, 0);
  });

  test("double-clicking the boundary restores the default height", async ({ page }) => {
    await mockApi(page);
    await openReview(page);

    const before = await bodyHeight(page);
    await dragBoundary(page, "commits", -150);
    expect(await bodyHeight(page)).toBeLessThan(before - 100);

    await page.locator('[data-section-resize="commits"]').dblclick();

    expect(await bodyHeight(page)).toBeCloseTo(before, 0);
    expect(await page.locator(".commit-section__body").evaluate((el) => el.style.maxHeight)).toBe("");
  });

  test("a drag cannot shrink a section below its floor", async ({ page }) => {
    await mockApi(page);
    await openReview(page);

    await dragBoundary(page, "commits", -5000);

    // SECTION_MIN_HEIGHT keeps a couple of rows visible rather than collapsing
    // the section outright -- that is what the header chevron is for.
    expect(await bodyHeight(page)).toBeCloseTo(80, 0);
    await expect(page.locator(".commit-item").first()).toBeVisible();
  });

  test("the file list takes back the height a section gives up", async ({ page }) => {
    await mockApi(page);
    await openReview(page);
    await page.locator(".diff-file-row").first().waitFor({ state: "visible", timeout: 10_000 });

    const filesBefore = await page.locator(".diff-files").boundingBox();
    expect(filesBefore).not.toBeNull();

    await dragBoundary(page, "commits", -150);

    const filesAfter = await page.locator(".diff-files").boundingBox();
    expect(filesAfter).not.toBeNull();
    expect(filesAfter!.height).toBeCloseTo(filesBefore!.height + 150, 0);
  });
});
