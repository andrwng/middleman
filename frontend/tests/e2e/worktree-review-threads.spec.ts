import { expect, test } from "@playwright/test";

import {
  LOCAL_DIFF_FILE_PATH,
  getReviewThreadsRequests,
  mockApi,
  seedReviewThread,
} from "./support/mockApi";

// Local worktree fixture constants — must match mockApi.ts.
const LOCAL_OWNER = "local";
const LOCAL_REPO = "myproject";
const LOCAL_ID = 7;

const filesRoute = `/pulls/${LOCAL_OWNER}/${LOCAL_REPO}/${LOCAL_ID}/files`;

// Stage 1's invariant, made executable end to end: every non-hidden
// review thread is openable from the threads list, always. The unit
// tests for this all mock the store boundary they meet at, so nothing
// else exercises diffStore -> load(at) -> the API -> placement.

test.beforeEach(async ({ page }) => {
  await mockApi(page);
});

test("a thread anchored to a line in the diff renders its card there", async ({ page }) => {
  seedReviewThread({ path: LOCAL_DIFF_FILE_PATH, line: 11, side: "RIGHT" });

  await page.goto(filesRoute);

  const file = page.locator(`.diff-file[data-file-path="${LOCAL_DIFF_FILE_PATH}"]`);
  await expect(file).toHaveCount(1);
  // The conversation itself, inline in the diff, at the anchored line.
  await expect(file.locator(".review-thread")).toContainText("seeded thread 1");
});

test("clicking a placed thread's row jumps to its line, not the fallback", async ({ page }) => {
  seedReviewThread({ path: LOCAL_DIFF_FILE_PATH, line: 11, side: "RIGHT" });

  await page.goto(filesRoute);
  const row = page.locator(".thread-item").filter({ hasText: LOCAL_DIFF_FILE_PATH });
  await expect(row).toHaveCount(1);
  await row.click();

  // scrollToDiffLine marks the line it landed on with a persistent
  // highlight, so this asserts the jump actually reached the line rather
  // than the file header.
  await expect(
    page.locator('.line-wrap--jump-highlight[data-anchor-line="11"][data-anchor-side="RIGHT"]'),
  ).toHaveCount(1);
  await expect(page.locator(".thread-item__fallback")).toHaveCount(0);
});

test("a thread whose file is not in the diff expands in place on click", async ({ page }) => {
  // Nothing in the mocked diff touches this path, so there is no
  // .diff-file to jump into and no inline card anywhere. Before the
  // floor existed, clicking this row did nothing at all.
  seedReviewThread({ path: "internal/api/deleted.go", line: 42, side: "RIGHT" });

  await page.goto(filesRoute);
  await expect(
    page.locator(`.diff-file[data-file-path="${LOCAL_DIFF_FILE_PATH}"]`),
  ).toHaveCount(1);
  await expect(page.locator(".thread-item__fallback")).toHaveCount(0);

  await page.locator(".thread-item").filter({ hasText: "deleted.go" }).click();

  const fallback = page.locator(".thread-item__fallback");
  await expect(fallback).toHaveCount(1);
  // The conversation and its reply box, not a placeholder.
  await expect(fallback).toContainText("seeded thread 1");
  await expect(fallback.locator("textarea")).toHaveCount(1);
  await expect(fallback.locator(".thread-item__reason")).toContainText("file unchanged here");
});

test("a thread whose line is not rendered expands in place rather than landing on a header", async ({ page }) => {
  // The file IS in the diff, but line 900 sits outside every rendered
  // hunk. The jump reports "pending" (a reveal was requested and the
  // view moved to the file header), and a CollapsedRegion never renders
  // a thread card -- so counting that as placed left the reader on a
  // header with no conversation and no reason.
  seedReviewThread({ path: LOCAL_DIFF_FILE_PATH, line: 900, side: "RIGHT" });

  await page.goto(filesRoute);
  await expect(
    page.locator(`.diff-file[data-file-path="${LOCAL_DIFF_FILE_PATH}"]`),
  ).toHaveCount(1);

  await page.locator(".thread-item").filter({ hasText: LOCAL_DIFF_FILE_PATH }).click();

  const fallback = page.locator(".thread-item__fallback");
  await expect(fallback).toHaveCount(1);
  await expect(fallback).toContainText("seeded thread 1");
  await expect(fallback.locator(".thread-item__reason")).toContainText("line not in this diff");
});

test("the threads list asks the server to resolve anchors against the diff's revision", async ({ page }) => {
  // C1's regression: the revision was sampled at mount, where it is
  // always "", so `at` never left the browser and the server-side
  // resolution was dead code. It has to arrive with the commits.
  const seen: string[] = [];
  await page.route(
    `**/api/v1/repos/${LOCAL_OWNER}/${LOCAL_REPO}/pulls/${LOCAL_ID}/review-threads*`,
    async (route) => {
      if (route.request().method() === "GET") {
        seen.push(new URL(route.request().url()).searchParams.get("at") ?? "");
      }
      await route.fallback();
    },
  );
  seedReviewThread({ path: LOCAL_DIFF_FILE_PATH, line: 11, side: "RIGHT" });

  await page.goto(filesRoute);
  await expect(
    page.locator(`.diff-file[data-file-path="${LOCAL_DIFF_FILE_PATH}"]`),
  ).toHaveCount(1);

  await expect
    .poll(() => seen.filter((at) => at !== ""))
    .toEqual(["abc1234def5678901234567890abcdef12345678"]);
  // Reading threads never writes any, whatever the resolution says.
  expect(getReviewThreadsRequests()).toEqual([]);
});
