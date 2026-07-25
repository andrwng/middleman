import { expect, test, type Page } from "@playwright/test";

import { getCreatedReviewThreads, getReviewThreadsRequests, mockApi } from "./support/mockApi";

// Local worktree fixture constants — must match mockApi.ts.
const LOCAL_OWNER = "local";
const LOCAL_REPO = "myproject";
const LOCAL_ID = 7;

const filesRoute = `/pulls/${LOCAL_OWNER}/${LOCAL_REPO}/${LOCAL_ID}/files`;
const docRoute = `/pulls/${LOCAL_OWNER}/${LOCAL_REPO}/${LOCAL_ID}/doc?path=README.md`;
const diagramRoute = `/pulls/${LOCAL_OWNER}/${LOCAL_REPO}/${LOCAL_ID}/doc?path=diagram.md`;
const linksRoute = `/pulls/${LOCAL_OWNER}/${LOCAL_REPO}/${LOCAL_ID}/doc?path=links.md`;

test.beforeEach(async ({ page }) => {
  await mockApi(page);
});

// Draft a comment on the doc's first H1 block (present in README.md,
// diagram.md, and links.md) via the same add-comment-btn -> gutter
// composer -> Save draft flow the "comment gutter" tests exercise below.
async function seedHeadingDraft(page: Page, text: string): Promise<void> {
  const heading = page.locator(".rmd-body > h1.rmd-block").first();
  await heading.hover();
  await heading.locator(".rmd-add-comment-btn").click();
  const composer = page.locator('[data-gutter-key^="composer:"]');
  await composer.locator("textarea").fill(text);
  await composer.locator("button", { hasText: "Save draft" }).click();
  await expect(composer).toHaveCount(0);
}

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

test("Docs palette: Ctrl-N / Ctrl-P move the selection", async ({ page }) => {
  await page.goto(filesRoute);
  await page.locator("button.doc-open").click();
  const palette = page.locator('[role="dialog"][aria-label="Open a doc"]');
  await expect(palette).toBeVisible();

  const options = page.locator('[role="option"]');
  await expect(options.first()).toHaveAttribute("aria-selected", "true");

  const input = palette.getByRole("textbox");
  await input.press("Control+n");
  await expect(options.nth(1)).toHaveAttribute("aria-selected", "true");
  await expect(options.first()).toHaveAttribute("aria-selected", "false");

  await input.press("Control+p");
  await expect(options.first()).toHaveAttribute("aria-selected", "true");
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

test("renders bold in a bullet whose bold phrase wraps across a source line", async ({ page }) => {
  await page.goto(docRoute);

  // Regression: inline markup that straddles a soft-wrapped source line
  // (a bold phrase leading a wrapped bullet) must render as one <strong>,
  // not as literal asterisks.
  const strong = page.locator(".rmd-body li strong");
  await expect(strong).toHaveCount(1);
  await expect(strong).toHaveText(
    "This leading phrase is bold and it wraps to a second source line",
  );
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

test("doc view: clicking an internal anchor link scrolls to that section", async ({ page }) => {
  await page.goto(linksRoute);
  await expect(page.locator(".rmd-body")).toContainText("Top");

  // The "## Details" heading (id="details") starts below the fold.
  const details = page.locator("#details");
  await expect(details).toHaveCount(1);
  await expect(details).not.toBeInViewport();

  // Clicking the internal link scrolls it into view (no bounce to the top).
  await page.getByRole("link", { name: "jump to details" }).click();
  await expect(details).toBeInViewport();
});

test("doc view: clicking a cross-doc link opens the linked doc in docs mode", async ({ page }) => {
  await page.goto(linksRoute);
  await expect(page.locator(".rmd-body")).toContainText("Top");

  // The cross-doc link's href is rewritten to the doc route (so a modified /
  // middle click opens it in a new tab natively).
  const link = page.getByRole("link", { name: "see the readme" });
  await expect(link).toHaveAttribute("href", /\/doc\?path=README\.md$/);

  // A plain click opens README.md in docs mode via client-side navigation.
  await link.click();
  await expect(page).toHaveURL(/\/doc\?path=README\.md$/);
  await expect(page.locator(".rmd-body")).toContainText("Hello");

  // Back returns to the linking doc (history navigation works).
  await page.goBack();
  await expect(page).toHaveURL(/\/doc\?path=links\.md$/);
  await expect(page.locator(".rmd-body")).toContainText("Top");
});

test("doc view: a cross-doc link with a #fragment opens the doc and scrolls to the section", async ({ page }) => {
  await page.goto(docRoute);
  await expect(page.locator(".rmd-body")).toContainText("Hello");

  // The link href carries both the target doc and the fragment.
  const link = page.getByRole("link", { name: "details section" });
  await expect(link).toHaveAttribute("href", /\/doc\?path=links\.md#details$/);

  // Clicking opens links.md AND scrolls to the (below-the-fold) #details section.
  await link.click();
  await expect(page).toHaveURL(/\/doc\?path=links\.md#details$/);
  await expect(page.locator("#details")).toBeInViewport();
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

test("doc review: save-only submit persists a thread and renders it in the gutter", async ({ page }) => {
  await page.addInitScript(() => {
    for (const k of Object.keys(localStorage)) {
      if (k.startsWith("diff-draft")) localStorage.removeItem(k);
    }
  });

  await page.goto(docRoute);
  await expect(page.locator(".rmd-body")).toContainText("Hello");

  await seedHeadingDraft(page, "does this still hold?");

  const reviewBtn = page.getByRole("button", { name: /^Review \(\d+\)$/ });
  await expect(reviewBtn).toHaveText("Review (1)");
  await reviewBtn.click();

  const panel = page.locator('[role="dialog"][aria-label="Finish review"]');
  await expect(panel).toBeVisible();

  // "Have Claude apply these changes" — uncheck it so this submit only
  // persists the thread (no agent turn).
  const agentCheckbox = panel.locator(".panel__agent input[type=checkbox]");
  await expect(agentCheckbox).toBeChecked();
  await agentCheckbox.uncheck();
  await panel.getByRole("button", { name: "Create review threads" }).click();

  // The panel closes on a successful submit.
  await expect(panel).toHaveCount(0);

  // The mock persisted exactly one thread, and did not receive a mode
  // (persist-only is the API-level default when `mode` is omitted).
  expect(getCreatedReviewThreads()).toHaveLength(1);
  expect(getReviewThreadsRequests().at(-1)?.mode).toBeUndefined();

  // The draft is gone — no composer, no pending-draft card, and the
  // Review button's count drops back to 0.
  await expect(page.locator('[data-gutter-key^="composer:"]')).toHaveCount(0);
  await expect(page.locator(".pending")).toHaveCount(0);
  await expect(page.getByRole("button", { name: /^Review \(\d+\)$/ })).toHaveText("Review (0)");

  // The persisted thread now renders as a gutter review-thread card.
  const card = page.locator(".comment-gutter .review-thread");
  await expect(card).toHaveCount(1);
  await expect(card).toContainText("does this still hold?");
  await expect(card.locator(".review-thread__badge")).toHaveText("Review");
});

test("doc review: apply submit sends act-immediately and still renders the thread", async ({ page }) => {
  await page.addInitScript(() => {
    for (const k of Object.keys(localStorage)) {
      if (k.startsWith("diff-draft")) localStorage.removeItem(k);
    }
  });

  await page.goto(docRoute);
  await expect(page.locator(".rmd-body")).toContainText("Hello");

  await seedHeadingDraft(page, "please simplify this section");

  await page.getByRole("button", { name: /^Review \(\d+\)$/ }).click();
  const panel = page.locator('[role="dialog"][aria-label="Finish review"]');
  await expect(panel).toBeVisible();

  // Leave "Have Claude apply these changes" checked (the default) and
  // submit via the apply verb.
  const agentCheckbox = panel.locator(".panel__agent input[type=checkbox]");
  await expect(agentCheckbox).toBeChecked();
  await panel.getByRole("button", { name: "Create & apply" }).click();

  await expect(panel).toHaveCount(0);

  // The create-threads POST carried mode: "act-immediately".
  const lastRequest = getReviewThreadsRequests().at(-1);
  expect(lastRequest?.mode).toBe("act-immediately");
  expect(lastRequest?.threads).toHaveLength(1);

  // The thread still renders as a gutter card regardless of the mode.
  const card = page.locator(".comment-gutter .review-thread");
  await expect(card).toHaveCount(1);
  await expect(card).toContainText("please simplify this section");
});

test("doc review: submitting from one doc only sends that doc's comment", async ({ page }) => {
  await page.addInitScript(() => {
    for (const k of Object.keys(localStorage)) {
      if (k.startsWith("diff-draft")) localStorage.removeItem(k);
    }
  });

  // Seed a draft on README.md.
  await page.goto(docRoute);
  await expect(page.locator(".rmd-body")).toContainText("Hello");
  await seedHeadingDraft(page, "readme comment");
  await expect(page.getByRole("button", { name: /^Review \(\d+\)$/ })).toHaveText("Review (1)");

  // Switch to diagram.md via the Docs palette (client-side navigation —
  // a second page.goto would re-run the localStorage-clearing init script
  // above and wipe the README draft just seeded) and seed a draft there.
  await page.locator("button.doc-open").click();
  await expect(page.locator('[role="dialog"][aria-label="Open a doc"]')).toBeVisible();
  await page
    .locator('[role="option"]')
    .filter({ hasText: "diagram.md" })
    .locator("a.palette-row-link")
    .click();
  await expect(page).toHaveURL(/[?&]path=diagram\.md$/);
  await expect(page.locator(".rmd-body")).toContainText("Diagram");
  await seedHeadingDraft(page, "diagram comment");
  await expect(page.getByRole("button", { name: /^Review \(\d+\)$/ })).toHaveText("Review (1)");

  // Back to README.md — its Review count is untouched by the other doc's draft.
  await page.locator("button.doc-open").click();
  await expect(page.locator('[role="dialog"][aria-label="Open a doc"]')).toBeVisible();
  await page
    .locator('[role="option"]')
    .filter({ hasText: "README.md" })
    .locator("a.palette-row-link")
    .click();
  await expect(page).toHaveURL(/[?&]path=README\.md$/);
  await expect(page.getByRole("button", { name: /^Review \(\d+\)$/ })).toHaveText("Review (1)");

  // Submit README's review only.
  await page.getByRole("button", { name: /^Review \(\d+\)$/ }).click();
  const panel = page.locator('[role="dialog"][aria-label="Finish review"]');
  await expect(panel).toBeVisible();
  await panel.locator(".panel__agent input[type=checkbox]").uncheck();
  await panel.getByRole("button", { name: "Create review threads" }).click();
  await expect(panel).toHaveCount(0);

  // Exactly one thread was sent, scoped to README.md.
  const lastRequest = getReviewThreadsRequests().at(-1);
  expect(lastRequest?.threads).toHaveLength(1);
  expect(lastRequest?.threads[0]?.path).toBe("README.md");
  expect(lastRequest?.threads[0]?.body).toBe("readme comment");

  // README.md's comment is now a review thread, not a pending draft.
  await expect(page.locator(".comment-gutter .review-thread")).toContainText("readme comment");
  await expect(page.locator(".pending")).toHaveCount(0);

  // diagram.md's pending draft is untouched by the scoped submit.
  await page.locator("button.doc-open").click();
  await expect(page.locator('[role="dialog"][aria-label="Open a doc"]')).toBeVisible();
  await page
    .locator('[role="option"]')
    .filter({ hasText: "diagram.md" })
    .locator("a.palette-row-link")
    .click();
  await expect(page).toHaveURL(/[?&]path=diagram\.md$/);
  await expect(page.getByRole("button", { name: /^Review \(\d+\)$/ })).toHaveText("Review (1)");
  await expect(page.locator(".pending")).toHaveCount(1);
  await expect(page.locator(".comment-gutter .review-thread")).toHaveCount(0);
});

// The working-tree-vs-HEAD diff route the doc pane fetches to compute
// per-line "uncommitted" highlighting. mockApi.ts defaults this route to
// an empty diff (no added lines for any file); each test below overrides
// it via page.route() so its diff expectations are self-contained and
// don't depend on — or leak into — any other test in this spec.
const diffRoute = `**/api/v1/repos/${LOCAL_OWNER}/${LOCAL_REPO}/pulls/${LOCAL_ID}/diff*`;

test("doc view: an uncommitted (working-tree-added) line's anchor is highlighted", async ({ page }) => {
  // Source line 3 of the mocked README.md content ("some text here") is a
  // plain, single-line paragraph outside any list/table, so it renders as
  // exactly one unambiguous .rmd-anchor[data-anchor-line="3"] span.
  await page.route(diffRoute, async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        stale: false,
        whitespace_only_count: 0,
        files: [
          {
            path: "README.md",
            hunks: [{ lines: [{ type: "add", new_num: 3 }] }],
          },
        ],
      }),
    });
  });

  await page.goto(docRoute);
  await expect(page.locator(".rmd-body")).toContainText("Hello");

  await expect(
    page.locator('.rmd-anchor[data-anchor-line="3"]').first(),
  ).toHaveClass(/rmd-uncommitted/);
});

test("doc view: a fully committed doc has no uncommitted highlight", async ({ page }) => {
  // Explicit no-added-lines diff response for README.md — mirrors the
  // shared mock's default, but scoped here so this test's intent (nothing
  // highlighted when there's nothing uncommitted) is self-contained.
  await page.route(diffRoute, async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ stale: false, whitespace_only_count: 0, files: [] }),
    });
  });

  await page.goto(docRoute);
  await expect(page.locator(".rmd-body")).toContainText("Hello");

  await expect(page.locator(".rmd-uncommitted")).toHaveCount(0);
});
