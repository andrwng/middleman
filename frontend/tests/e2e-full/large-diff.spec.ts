import {
  expect,
  request as playwrightRequest,
  test,
  type APIRequestContext,
  type Page,
} from "@playwright/test";
import { startIsolatedE2EServer, type IsolatedE2EServer } from "./support/e2eServer";

// acme/widgets #2 is seeded by testutil.SetupLargeDiffRepo with a generated
// diff: 700 src files of 80 lines, 40 gen/ files of 400 lines, a deleted
// 2000-line file and a modified 1800-line file (+900/-900). That is far
// above the lazy threshold, so the review pane loads the /files summary
// first and fetches hunks per file through /diff/hunks.

const LARGE_PR = "/pulls/acme/widgets/2/files";
const SMALL_PR = "/pulls/acme/widgets/1/files";
const LARGE_API = "/api/v1/repos/acme/widgets/pulls/2";
const TOTAL_FILES = 742;
const GEN_FILES = 40;
const DELETED_PATH = "legacy/schema.sql";
const MODIFIED_PATH = "internal/big_table.go";
const PKG3_FILES = 35; // i % 20 == 3 for i in [0, 700)

function sidebarRow(page: Page, path: string) {
  return page.locator(`.diff-file-row[title="${path}"]`);
}

function diffFile(page: Page, path: string) {
  return page.locator(`.diff-file[data-file-path="${path}"]`);
}

function diffLine(page: Page, path: string, line: number) {
  return diffFile(page, path).locator(
    `[data-anchor-line="${line}"][data-anchor-side="RIGHT"]`,
  );
}

async function waitForSidebar(page: Page): Promise<void> {
  await expect(page.locator(".diff-file-row")).toHaveCount(TOTAL_FILES, { timeout: 15_000 });
}

// The last file in diff order, read from the DOM rather than assumed, so the
// test follows whatever sort order the review pane uses.
async function lastFilePath(page: Page): Promise<string> {
  const path = await page.locator(".diff-file").last().getAttribute("data-file-path");
  expect(path).toBeTruthy();
  return path!;
}

// Records the pathname of every /diff (full) and /diff/hunks request for a PR.
function trackDiffRequests(page: Page, prNumber: number) {
  const prefix = `/api/v1/repos/acme/widgets/pulls/${prNumber}`;
  const seen = { full: 0, hunks: 0 };
  page.on("request", (req) => {
    const path = new URL(req.url()).pathname;
    if (path === `${prefix}/diff`) seen.full++;
    if (path === `${prefix}/diff/hunks`) seen.hunks++;
  });
  return seen;
}

test.describe("large diff review", () => {
  test.beforeEach(async ({ page }) => {
    await page.addInitScript(() => {
      localStorage.removeItem("diff-hide-whitespace");
    });
  });

  test("the sidebar lists every file before any hunks arrive", async ({ page }) => {
    let release!: () => void;
    const released = new Promise<void>((resolve) => { release = resolve; });
    let held = 0;
    await page.route(`**${LARGE_API}/diff/hunks`, async (route) => {
      held++;
      await released;
      await route.continue();
    });

    await page.goto(LARGE_PR);
    await waitForSidebar(page);

    // Hunk requests went out and none of them has resolved yet.
    await expect.poll(() => held).toBeGreaterThan(0);
    await expect(page.locator(".line-wrap")).toHaveCount(0);
    await expect(diffFile(page, "gen/file00.pb.go")).toBeVisible();

    release();
    await expect(page.locator(".line-wrap").first()).toBeVisible({ timeout: 10_000 });
  });

  test("the large PR never requests the full diff; the small PR only does", async ({ page }) => {
    const large = trackDiffRequests(page, 2);
    await page.goto(LARGE_PR);
    await waitForSidebar(page);
    await expect(page.locator(".line-wrap").first()).toBeVisible({ timeout: 10_000 });
    await expect.poll(() => large.hunks).toBeGreaterThan(0);
    expect(large.full).toBe(0);

    const small = trackDiffRequests(page, 1);
    await page.goto(SMALL_PR);
    await expect(page.locator(".line-wrap").first()).toBeVisible({ timeout: 10_000 });
    await expect.poll(() => small.full).toBe(1);
    // Give a stray second request time to show up before counting.
    await page.waitForTimeout(500);
    expect(small.full).toBe(1);
    expect(small.hunks).toBe(0);
    expect(large.full).toBe(0);
  });

  test("scrolling to the bottom keeps the rendered line count bounded", async ({ page }) => {
    test.setTimeout(120_000);
    await page.goto(LARGE_PR);
    await waitForSidebar(page);
    await expect(page.locator(".line-wrap").first()).toBeVisible({ timeout: 10_000 });

    const area = page.locator(".diff-area");
    // Lines whose box overlaps the diff area's visible rect: proof the
    // window has caught up with the scroll position.
    const visibleLines = () => area.evaluate((el) => {
      const r = el.getBoundingClientRect();
      let n = 0;
      for (const line of el.querySelectorAll(".line-wrap")) {
        const b = line.getBoundingClientRect();
        if (b.bottom > r.top && b.top < r.bottom) n++;
      }
      return n;
    });

    const lineCount = () => page.locator(".line-wrap").count();

    // Fine steps first, two viewports at a time through the first ~3200
    // lines (20px each): without unmounting behind the viewport, everything
    // passed would stay rendered and the count would cross the ceiling.
    for (let i = 0; i < 70; i++) {
      await area.evaluate((el) => { el.scrollTop += el.clientHeight * 2; });
      await expect.poll(visibleLines, { timeout: 10_000 }).toBeGreaterThan(0);
      expect(await lineCount()).toBeLessThan(3000);
    }

    // Then coarse steps from here to the bottom of the ~1.7M px diff.
    const steps = 30;
    const start = await area.evaluate((el) => el.scrollTop);
    for (let i = 1; i <= steps; i++) {
      await area.evaluate((el, { from, step, total }) => {
        const max = el.scrollHeight - el.clientHeight;
        el.scrollTop = step === total ? max : Math.round(from + ((max - from) * step) / total);
      }, { from: start, step: i, total: steps });
      await expect.poll(visibleLines, { timeout: 10_000 }).toBeGreaterThan(0);
      expect(await lineCount()).toBeLessThan(3000);
    }

    // Landed at the very end: the last file's last line is on screen. The
    // summary can still grow as estimated heights settle, so re-pin the
    // bottom until the line shows.
    const last = await lastFilePath(page);
    await expect(async () => {
      await area.evaluate((el) => { el.scrollTop = el.scrollHeight; });
      await expect(diffLine(page, last, 80)).toBeInViewport({ timeout: 1_000 });
    }).toPass({ timeout: 15_000 });
    expect(await lineCount()).toBeLessThan(3000);
  });

  test("jumping to a draft in a collapsed, unloaded, unmounted last file lands on the line", async ({ page, request }) => {
    const commitsResp = await request.get(`${LARGE_API}/commits`);
    expect(commitsResp.ok()).toBe(true);
    const { commits } = (await commitsResp.json()) as { commits: { sha: string }[] };
    const headSha = commits[0]!.sha;

    // The last file in diff order, as the page renders it.
    await page.goto(LARGE_PR);
    await waitForSidebar(page);
    const target = await lastFilePath(page);

    // Seed a draft on its last line and collapse it by hand, then load
    // fresh so nothing about the file has been fetched or mounted.
    await page.evaluate(([path, sha]) => {
      localStorage.setItem("diff-draft-reviews", JSON.stringify({
        "acme/widgets#2": {
          body: "",
          event: "COMMENT",
          comments: [{
            id: "draft-large-1",
            path,
            line: 80,
            side: "RIGHT",
            commitSha: sha,
            body: "draft on the last line of the last file",
            createdAt: "2026-04-01T00:00:00Z",
          }],
        },
      }));
      localStorage.setItem("diff-collapse-overrides", JSON.stringify({
        "acme/widgets#2": { [path!]: true },
      }));
    }, [target, headSha]);

    const hunkPaths: string[] = [];
    page.on("request", (req) => {
      if (new URL(req.url()).pathname === `${LARGE_API}/diff/hunks`) {
        const body = req.postDataJSON() as { paths?: string[] } | null;
        hunkPaths.push(...(body?.paths ?? []));
      }
    });

    await page.reload();
    await waitForSidebar(page);
    await expect(page.locator(".line-wrap").first()).toBeVisible({ timeout: 10_000 });

    await expect(page.locator(".diff-area")).toHaveJSProperty("scrollTop", 0);
    const file = diffFile(page, target);
    await expect(file.locator(".collapse-chevron--collapsed")).toHaveCount(1);
    await expect(file).toHaveAttribute("data-body-mounted", "false");
    expect(hunkPaths).not.toContain(target);

    await page.locator(".draft-item__main").click();

    const line = diffLine(page, target, 80);
    await expect(line).toBeInViewport({ timeout: 15_000 });
    await expect(line).toHaveClass(/line-wrap--jump-highlight/);
    await expect(file).toHaveAttribute("data-body-mounted", "true");
    expect(hunkPaths).toContain(target);
  });

  test("collapsing a directory persists across a reload", async ({ page }) => {
    await page.goto(LARGE_PR);
    await waitForSidebar(page);

    const pkg3Rows = page.locator('.diff-file-row[title^="src/pkg3/"]');
    await expect(pkg3Rows).toHaveCount(PKG3_FILES);
    await expect(page.locator('.diff-file-row.file-row--collapsed[title^="src/pkg3/"]')).toHaveCount(0);

    await page.getByRole("button", { name: "Collapse files in src/pkg3", exact: true }).click();

    const collapsedRows = page.locator('.diff-file-row.file-row--collapsed[title^="src/pkg3/"]');
    const collapsedFiles = page.locator(
      '.diff-file[data-file-path^="src/pkg3/"]:has(.collapse-chevron--collapsed)',
    );
    await expect(collapsedRows).toHaveCount(PKG3_FILES);
    await expect(collapsedFiles).toHaveCount(PKG3_FILES);
    // Neighbouring directories are untouched.
    await expect(page.locator('.diff-file-row.file-row--collapsed[title^="src/pkg13/"]')).toHaveCount(0);

    await page.reload();
    await waitForSidebar(page);
    await expect(collapsedRows).toHaveCount(PKG3_FILES);
    await expect(collapsedFiles).toHaveCount(PKG3_FILES);
    await expect(page.locator('.diff-file-row.file-row--collapsed[title^="src/pkg13/"]')).toHaveCount(0);
  });
});

// Collapse globs are stored per repo on the server, so these tests get their
// own server: adding gen/** on the shared one would change what every other
// test sees for acme/widgets. Serial so the reset in beforeEach is not raced.
test.describe("large diff collapse globs", () => {
  test.describe.configure({ mode: "serial" });

  let isolated: IsolatedE2EServer;
  let api: APIRequestContext;

  test.beforeAll(async () => {
    isolated = await startIsolatedE2EServer();
    api = await playwrightRequest.newContext({ baseURL: isolated.info.base_url });
  });

  test.afterAll(async () => {
    await api?.dispose();
    await isolated?.stop();
  });

  async function putGlobs(globs: string[]): Promise<void> {
    const resp = await api.put(`${LARGE_API}/diff-collapse-globs`, { data: { globs } });
    expect(resp.ok()).toBe(true);
  }

  test.beforeEach(async () => {
    await putGlobs([]);
  });

  test("default collapse badges, then gen/** from the sidebar marks gen files pattern", async ({ page }) => {
    await page.goto(`${isolated.info.base_url}${LARGE_PR}`);
    await waitForSidebar(page);

    const badge = (path: string) => sidebarRow(page, path).locator(".diff-file-collapse-badge");
    await expect(badge(DELETED_PATH)).toHaveText("deleted");
    await expect(badge(MODIFIED_PATH)).toHaveText("large");
    await expect(diffFile(page, DELETED_PATH).locator(".collapse-chevron--collapsed")).toHaveCount(1);
    await expect(diffFile(page, MODIFIED_PATH).locator(".collapse-chevron--collapsed")).toHaveCount(1);

    // gen files are 400 lines each: under the large threshold, so they
    // start expanded until a glob says otherwise.
    const genBadges = page.locator('.diff-file-row[title^="gen/"] .diff-file-collapse-badge');
    await expect(page.locator('.diff-file-row[title^="gen/"]')).toHaveCount(GEN_FILES);
    await expect(genBadges).toHaveCount(0);

    await page.getByRole("button", { name: "Always collapse gen/** in this repo", exact: true }).click();
    await expect(page.locator(".globs__item", { hasText: "gen/**" })).toContainText(`${GEN_FILES} files`);
    await expect(genBadges).toHaveCount(GEN_FILES);

    await page.reload();
    await waitForSidebar(page);
    await expect(genBadges).toHaveCount(GEN_FILES);
    for (const text of await genBadges.allTextContents()) expect(text).toBe("pattern");
    await expect(
      page.locator('.diff-file[data-file-path^="gen/"]:has(.collapse-chevron--collapsed)'),
    ).toHaveCount(GEN_FILES);
    // The other default reasons still hold alongside the pattern.
    await expect(badge(DELETED_PATH)).toHaveText("deleted");
    await expect(badge(MODIFIED_PATH)).toHaveText("large");

    const stored = await api.get(`${LARGE_API}/diff-collapse-globs`);
    expect(((await stored.json()) as { globs: string[] }).globs).toEqual(["gen/**"]);
  });

  test("expanding a pattern-collapsed file shows all of its lines", async ({ page }) => {
    await putGlobs(["gen/**"]);
    await page.goto(`${isolated.info.base_url}${LARGE_PR}`);
    await waitForSidebar(page);

    const path = "gen/file00.pb.go";
    await expect(sidebarRow(page, path).locator(".diff-file-collapse-badge")).toHaveText("pattern");
    const file = diffFile(page, path);
    await expect(file.locator(".collapse-chevron--collapsed")).toHaveCount(1);
    await expect(file.locator(".line-wrap")).toHaveCount(0);

    await file.locator(".file-header__collapse").click();
    await expect(file.locator(".collapse-chevron--collapsed")).toHaveCount(0);
    await expect(diffLine(page, path, 1)).toBeVisible({ timeout: 10_000 });

    // Scroll down through the file to its end.
    const last = diffLine(page, path, 400);
    await last.scrollIntoViewIfNeeded();
    await expect(last).toBeInViewport();
    await expect(last).toContainText("var Gen00Field400 = 400 // generated");
    await expect(file.locator(".line-wrap")).toHaveCount(400);
  });
});
