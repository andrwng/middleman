import { expect, test } from "@playwright/test";

import { mockApi } from "./support/mockApi";

// Local worktree fixture constants — must match mockApi.ts.
const LOCAL_OWNER = "local";
const LOCAL_REPO = "myproject";
const LOCAL_ID = 7;

const filesRoute = `/pulls/${LOCAL_OWNER}/${LOCAL_REPO}/${LOCAL_ID}/files`;
const docRoute = `/pulls/${LOCAL_OWNER}/${LOCAL_REPO}/${LOCAL_ID}/doc?path=README.md`;
const diagramRoute = `/pulls/${LOCAL_OWNER}/${LOCAL_REPO}/${LOCAL_ID}/doc?path=diagram.md`;

test.beforeEach(async ({ page }) => {
  await mockApi(page);
});

test("Docs trigger opens palette listing README.md", async ({ page }) => {
  await page.goto(filesRoute);

  // The "Docs" tab button is only rendered for local-source PRs.
  const docsBtn = page.locator("button.doc-open");
  await expect(docsBtn).toBeVisible();
  await docsBtn.click();

  // Palette dialog opens.
  const palette = page.locator('[role="dialog"][aria-label="Open a doc"]');
  await expect(palette).toBeVisible();

  // README.md appears as a palette option.
  await expect(
    page.locator('[role="option"]').filter({ hasText: "README.md" }),
  ).toBeVisible();
});

test("picking README.md navigates to doc URL and renders heading", async ({ page }) => {
  await page.goto(filesRoute);

  // Open the palette.
  await page.locator("button.doc-open").click();
  await expect(
    page.locator('[role="dialog"][aria-label="Open a doc"]'),
  ).toBeVisible();

  // Click the README.md row — plain click triggers in-app navigation.
  const row = page.locator('[role="option"]').filter({ hasText: "README.md" });
  await expect(row).toBeVisible();
  await row.locator("a.palette-row-link").click();

  // URL should reflect the doc route.
  await expect(page).toHaveURL(
    new RegExp(`/pulls/${LOCAL_OWNER}/${LOCAL_REPO}/${LOCAL_ID}/doc`),
  );
  await expect(page).toHaveURL(/[?&]path=README\.md/);

  // The doc path is shown in the header.
  await expect(page.locator(".doc-path")).toContainText("README.md");

  // The rendered heading "Hello" from "# Hello\n\nsome text here" is visible.
  await expect(page.locator(".rmd-body")).toContainText("Hello");
});

test("palette row exposes a working new-tab href", async ({ page }) => {
  await page.goto(filesRoute);

  await page.locator("button.doc-open").click();
  await expect(
    page.locator('[role="dialog"][aria-label="Open a doc"]'),
  ).toBeVisible();

  const row = page.locator('[role="option"]').filter({ hasText: "README.md" });
  await expect(row).toBeVisible();

  // The new-tab anchor (↗) must have an href pointing to the doc URL.
  const newTabLink = row.locator("a.palette-row-newtab");
  const href = await newTabLink.getAttribute("href");
  expect(href).toBeTruthy();
  expect(href).toContain(`/pulls/${LOCAL_OWNER}/${LOCAL_REPO}/${LOCAL_ID}/doc`);
  expect(href).toContain("path=README.md");
  // It must open in a new tab.
  await expect(newTabLink).toHaveAttribute("target", "_blank");
});

test("cold-load: page.goto doc URL renders doc standalone", async ({ page }) => {
  await page.goto(docRoute);

  // DocReviewSurface renders the path and the rendered markdown body.
  await expect(page.locator(".doc-path")).toContainText("README.md");
  await expect(page.locator(".rmd-body")).toContainText("Hello");

  // Back button (← Review) is present.
  await expect(page.locator("button.doc-back")).toBeVisible();
});

test("doc-newtab link on DocReviewSurface has correct href", async ({ page }) => {
  await page.goto(docRoute);

  const newTabLink = page.locator("a.doc-newtab");
  await expect(newTabLink).toBeVisible();
  const href = await newTabLink.getAttribute("href");
  expect(href).toBeTruthy();
  expect(href).toContain(`/pulls/${LOCAL_OWNER}/${LOCAL_REPO}/${LOCAL_ID}/doc`);
  expect(href).toContain("path=README.md");
  await expect(newTabLink).toHaveAttribute("target", "_blank");
});

test("back button on DocReviewSurface navigates to /files", async ({ page }) => {
  await page.goto(docRoute);

  await page.locator("button.doc-back").click();
  await expect(page).toHaveURL(new RegExp(`${filesRoute}$`));
});

test("comment gutter: gutter container present and composer opens in gutter on heading block", async ({ page }) => {
  // Clear any leftover draft state from prior runs.
  await page.addInitScript(() => {
    for (const k of Object.keys(localStorage)) {
      if (k.startsWith("diff-draft")) localStorage.removeItem(k);
    }
  });

  await page.goto(docRoute);

  // Wait for the rendered markdown to appear.
  await expect(page.locator(".rmd-body")).toContainText("Hello");

  // Assertion 1: gutter layout mode — the rmd-gutter-col column is present.
  const gutterCol = page.locator(".rmd-gutter-col");
  await expect(gutterCol).toBeVisible();

  // The view root carries the gutter grid class.
  await expect(page.locator(".rmd-view--gutter")).toBeVisible();

  // Assertion 2: clicking the add-comment button on the heading block opens
  // the composer IN THE GUTTER (a data-gutter-key entry), not at the bottom
  // of the document and not inline in the prose.
  //
  // The heading block ("# Hello") is the first .rmd-block child of .rmd-body.
  // The .rmd-add-comment-btn inside it is opacity:0 until hover.
  const headingBlock = page.locator(".rmd-body > h1.rmd-block").first();
  await expect(headingBlock).toBeVisible();

  // Hover to reveal the affordance buttons.
  await headingBlock.hover();

  // The add-comment button becomes clickable after hover.
  const addBtn = headingBlock.locator(".rmd-add-comment-btn");
  await expect(addBtn).toBeVisible();
  await addBtn.click();

  // Composer must appear as a gutter entry (data-gutter-key starting with "composer:").
  const composerEntry = page.locator('[data-gutter-key^="composer:"]');
  await expect(composerEntry).toBeVisible();

  // The composer must NOT be rendered in the prose body (no .rmd-composer-wrap in body).
  await expect(page.locator(".rmd-body .rmd-composer-wrap")).toHaveCount(0);

  // Assertion 3: fill the composer and save; exactly ONE card appears in the gutter.
  const textarea = composerEntry.locator("textarea");
  await expect(textarea).toBeVisible();
  await textarea.fill("Test gutter comment");

  const saveBtn = composerEntry.locator("button", { hasText: "Save draft" });
  await expect(saveBtn).toBeEnabled();
  await saveBtn.click();

  // After save, the composer entry should be gone.
  await expect(composerEntry).toHaveCount(0);

  // Exactly one cards entry should be present in the gutter for the heading block.
  // Cards entries have data-gutter-key starting with "block:".
  const cardEntries = page.locator('[data-gutter-key^="block:"]');
  await expect(cardEntries).toHaveCount(1);

  // Assertion 4: the heading block carries the .rmd-block--commented marker.
  await expect(headingBlock).toHaveClass(/rmd-block--commented/);
});

test("comment gutter: hovering a card highlights its source block", async ({ page }) => {
  await page.addInitScript(() => {
    for (const k of Object.keys(localStorage)) {
      if (k.startsWith("diff-draft")) localStorage.removeItem(k);
    }
  });

  await page.goto(docRoute);
  await expect(page.locator(".rmd-body")).toContainText("Hello");

  // Create a comment on the heading block.
  const headingBlock = page.locator(".rmd-body > h1.rmd-block").first();
  await headingBlock.hover();
  await headingBlock.locator(".rmd-add-comment-btn").click();
  const composer = page.locator('[data-gutter-key^="composer:"]');
  await composer.locator("textarea").fill("link me");
  await composer.locator("button", { hasText: "Save draft" }).click();

  // The card lives in the gutter; the source block is not highlighted yet.
  const card = page.locator('[data-gutter-key^="block:"]');
  await expect(card).toHaveCount(1);

  // The card aligns with the TOP of its source block, not the bottom: its top
  // is near the block's top (within one block height), not a block-height below.
  const cardBox = await card.boundingBox();
  const blockBox = await headingBlock.boundingBox();
  expect(cardBox).not.toBeNull();
  expect(blockBox).not.toBeNull();
  const topDelta = cardBox!.y - blockBox!.y;
  expect(topDelta).toBeGreaterThan(-8);
  expect(topDelta).toBeLessThan(blockBox!.height);

  await expect(headingBlock).not.toHaveClass(/rmd-block--linked/);

  // Hovering the card highlights the source block.
  await card.hover();
  await expect(headingBlock).toHaveClass(/rmd-block--linked/);

  // The per-card "scroll to source" button is present.
  await expect(card.locator(".comment-gutter__jump")).toHaveCount(1);
});

test("doc view: a mermaid code block renders as an embedded SVG diagram", async ({ page }) => {
  await page.goto(diagramRoute);
  await expect(page.locator(".rmd-body")).toContainText("Diagram");

  // The mermaid fenced block renders to an inline SVG (mermaid runs in-browser,
  // lazy-loaded). The raw source <pre> is replaced once the SVG is ready.
  await expect(page.locator(".rmd-mermaid__svg svg")).toBeVisible({ timeout: 10000 });
  await expect(page.locator(".rmd-mermaid__src")).toHaveCount(0);
});

test("comment gutter: dragging the divider resizes the gutter width (horizontal)", async ({ page }) => {
  // Start from the default width regardless of prior runs.
  await page.addInitScript(() => localStorage.removeItem("rmd-gutter-width"));

  await page.goto(docRoute);
  await expect(page.locator(".rmd-body")).toContainText("Hello");

  const gutterCol = page.locator(".rmd-gutter-col");
  await expect(gutterCol).toBeVisible();

  // The divider handle is present and grabbable.
  const divider = page.locator(".rmd-gutter-resize");
  await expect(divider).toBeVisible();

  const before = await gutterCol.boundingBox();
  expect(before).not.toBeNull();

  // Drag the divider left (toward the body) by 120px to widen the gutter.
  // hover() waits for actionability so the pointerdown reliably registers even
  // under parallel load; poll() tolerates the reactive width update settling.
  await divider.hover();
  const handle = await divider.boundingBox();
  expect(handle).not.toBeNull();
  const targetX = handle!.x + handle!.width / 2 - 120;
  const y = handle!.y + Math.min(handle!.height / 2, 60);
  await page.mouse.down();
  await page.mouse.move(targetX, y, { steps: 10 });
  await page.mouse.move(targetX, y); // settle the final position
  await page.mouse.up();

  // The gutter column is meaningfully wider (allow for clamping/rounding).
  await expect
    .poll(async () => (await gutterCol.boundingBox())?.width ?? 0)
    .toBeGreaterThan(before!.width + 80);

  // The chosen width is persisted for next time.
  const stored = await page.evaluate(() => localStorage.getItem("rmd-gutter-width"));
  expect(Number(stored)).toBeGreaterThan(before!.width);
});
