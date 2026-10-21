import { expect, test, type Page } from "@playwright/test";
import type { DiffResult, FilesResult } from "@middleman/ui/api/types";

// Clicking a draft in the sidebar's Drafts list goes to the draft's anchor
// even when it was written against a commit other than the one on screen:
// the diff switches to that commit first, then scrolls to the line. It used
// to only query the DOM, so a draft on another commit did nothing.

const NEWEST = "aaa1111".repeat(5).slice(0, 40);
const OLDEST = "ccc3333".repeat(5).slice(0, 40);
const SHAS = [NEWEST, OLDEST]; // the API returns commits newest-first
const PATH = "internal/server/handler.go";

const diff: DiffResult = {
  stale: false,
  whitespace_only_count: 0,
  files: [
    {
      path: PATH,
      old_path: PATH,
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

// Drafts persist in localStorage keyed by PR, so seeding it is how a test
// starts with a draft already written.
async function seedDraft(page: Page, commitSha: string): Promise<void> {
  await page.addInitScript(
    ([path, sha]) => {
      localStorage.setItem(
        "diff-draft-reviews",
        JSON.stringify({
          "acme/widgets#1": {
            body: "",
            event: "COMMENT",
            comments: [
              {
                id: "draft-1",
                path,
                line: 2,
                side: "RIGHT",
                commitSha: sha,
                body: "drafted on the oldest commit",
                createdAt: "2026-04-01T00:00:00Z",
              },
            ],
          },
        }),
      );
    },
    [PATH, commitSha],
  );
}

function rowFor(page: Page, sha: string) {
  return page.locator(`.commit-item[data-commit-sha="${sha}"]`);
}

test.describe("jumping to a draft", () => {
  test("a draft on another commit switches the diff to that commit", async ({ page }) => {
    const diffQueries = await mockApi(page);
    await seedDraft(page, OLDEST);
    await page.goto("/pulls/acme/widgets/1/files");
    await page.locator(".commit-item").first().waitFor({ state: "visible", timeout: 10_000 });

    // Head scope to start: no single commit selected.
    await expect(page.locator(".commit-item--active")).toHaveCount(0);

    await page.locator(".draft-item__main").click();

    await expect.poll(() => diffQueries.at(-1)).toBe(`?commit=${OLDEST}`);
    await expect(rowFor(page, OLDEST)).toHaveClass(/commit-item--active/);
    await expect(
      page.locator(
        `.diff-file[data-file-path="${PATH}"] .line-wrap[data-anchor-line="2"][data-anchor-side="RIGHT"]`,
      ),
    ).toBeInViewport();
  });

  test("a draft on the head commit returns from a commit scope to head", async ({ page }) => {
    const diffQueries = await mockApi(page);
    await seedDraft(page, NEWEST);
    await page.goto("/pulls/acme/widgets/1/files");
    await page.locator(".commit-item").first().waitFor({ state: "visible", timeout: 10_000 });

    await rowFor(page, OLDEST).click();
    await expect.poll(() => diffQueries.at(-1)).toBe(`?commit=${OLDEST}`);

    await page.locator(".draft-item__main").click();

    await expect(page.locator(".commit-item--active")).toHaveCount(0);
    await expect.poll(() => diffQueries.at(-1)).not.toContain("commit=");
  });
});
