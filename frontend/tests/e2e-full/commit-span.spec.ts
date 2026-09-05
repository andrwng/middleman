import { expect, test, type Page } from "@playwright/test";
import type { DiffResult, FilesResult } from "@middleman/ui/api/types";

// Selecting more than one commit already worked -- click one, shift-click
// another, and the diff becomes the net change across that span. Nothing
// advertised it, and nothing tested it. These do both.

const NEWEST = "aaa1111".repeat(5).slice(0, 40);
const MIDDLE = "bbb2222".repeat(5).slice(0, 40);
const OLDEST = "ccc3333".repeat(5).slice(0, 40);
const SHAS = [NEWEST, MIDDLE, OLDEST]; // the API returns commits newest-first

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

// Returns the query string of every diff request the page makes, so a test can
// assert which scope the store actually asked the server for.
async function mockApi(page: Page): Promise<string[]> {
  const diffQueries: string[] = [];
  await page.route("**/api/v1/repos/acme/widgets/pulls/1/files", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(filesFromDiff(diff)),
    });
  });
  await page.route("**/api/v1/repos/acme/widgets/pulls/1/diff*", async (route) => {
    diffQueries.push(new URL(route.request().url()).search);
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
        commits: SHAS.map((sha, i) => ({
          sha,
          message: `commit number ${i}`,
          body: "",
          authored_at: "2026-04-01T00:00:00Z",
          author_name: "alice",
        })),
      }),
    });
  });
  return diffQueries;
}

async function openReview(page: Page): Promise<void> {
  await page.goto("/pulls/acme/widgets/1/files");
  await page.locator(".commit-item").first().waitFor({ state: "visible", timeout: 10_000 });
}

function rowFor(page: Page, sha: string) {
  return page.locator(`.commit-item[data-commit-sha="${sha}"]`);
}

test.describe("spanning a range of commits", () => {
  test("shift-click spans the diff across the commits between the two clicks", async ({ page }) => {
    const diffQueries = await mockApi(page);
    await openReview(page);

    await rowFor(page, OLDEST).click();
    await expect(page.locator(".commit-item--active")).toHaveCount(1);

    await rowFor(page, NEWEST).click({ modifiers: ["Shift"] });

    // Every commit in the span is marked, including the one in the middle that
    // was never clicked.
    await expect(page.locator(".commit-item--active")).toHaveCount(3);
    await expect(rowFor(page, MIDDLE)).toHaveClass(/commit-item--active/);

    // And the server was asked for the span, not for a single commit.
    await expect
      .poll(() => diffQueries.at(-1))
      .toBe(`?from=${OLDEST}&to=${NEWEST}`);
  });

  test("the span is reported as a range, and resettable", async ({ page }) => {
    await mockApi(page);
    await openReview(page);

    await rowFor(page, OLDEST).click();
    await rowFor(page, NEWEST).click({ modifiers: ["Shift"] });

    await expect(page.locator(".scope-pill").first())
      .toContainText(`${OLDEST.slice(0, 7)}..${NEWEST.slice(0, 7)}`);
  });

  test("a row advertises spanning only once a commit is selected", async ({ page }) => {
    await mockApi(page);
    await openReview(page);

    // Nothing selected: a shift-click would just select the row, so no row
    // promises a span.
    for (const sha of SHAS) {
      expect(await rowFor(page, sha).getAttribute("title")).not.toContain("Shift-click");
    }

    await rowFor(page, OLDEST).click();

    // Now the other rows name the anchor they would span from...
    const hint = `Shift-click to diff ${OLDEST.slice(0, 7)} through this commit`;
    expect(await rowFor(page, NEWEST).getAttribute("title")).toContain(hint);
    expect(await rowFor(page, MIDDLE).getAttribute("title")).toContain(hint);
    // ...while the anchor's own row does not, since that span is a no-op.
    expect(await rowFor(page, OLDEST).getAttribute("title")).not.toContain("Shift-click");
    // The commit message stays in the tooltip either way.
    expect(await rowFor(page, NEWEST).getAttribute("title")).toContain("commit number 0");
  });

  test("the hint goes away once a span is active, since shift-click no longer extends it", async ({ page }) => {
    await mockApi(page);
    await openReview(page);

    await rowFor(page, OLDEST).click();
    await rowFor(page, NEWEST).click({ modifiers: ["Shift"] });
    await expect(page.locator(".commit-item--active")).toHaveCount(3);

    // From a range, a shift-click resets to a single commit rather than
    // growing the span, so no row claims otherwise.
    for (const sha of SHAS) {
      expect(await rowFor(page, sha).getAttribute("title")).not.toContain("Shift-click");
    }
  });
});
