import { expect, test } from "@playwright/test";

import { mockApi } from "./support/mockApi";

// Local worktree fixture constants — must match mockApi.ts.
const LOCAL_OWNER = "local";
const LOCAL_REPO = "myproject";
const LOCAL_ID = 7;

const filesRoute = `/pulls/${LOCAL_OWNER}/${LOCAL_REPO}/${LOCAL_ID}/files`;
const docRoute = `/pulls/${LOCAL_OWNER}/${LOCAL_REPO}/${LOCAL_ID}/doc?path=README.md`;

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
