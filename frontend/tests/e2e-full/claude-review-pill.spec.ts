import { expect, test, type Locator } from "@playwright/test";

// cmd/e2e-server seeds a finished Review with Claude run, with no
// suggestions, on acme/widgets#7. No other e2e test opens #7.
const PR_FILES = "/pulls/acme/widgets/7/files";
const LATEST_RUN = "/api/v1/repos/acme/widgets/pulls/7/ai-review-runs/latest";
const DISMISSED_RUNS_KEY = "claude-review-dismissed-runs";

test("a dismissed Claude review pill stays gone after a reload", async ({ page }) => {
  await page.goto(PR_FILES);
  const pill = page.getByRole("button", { name: /Done · 0 suggestions/ });
  const dismiss = page.getByRole("button", { name: "Dismiss Claude review" });
  await expect(pill).toBeVisible();

  await dismiss.click();
  await expect(pill).toHaveCount(0);

  const latest = page.waitForResponse((r) => new URL(r.url()).pathname === LATEST_RUN);
  await page.reload();
  await latest;
  await expect(pill).toHaveCount(0);
  await expect(dismiss).toHaveCount(0);

  // Control: the run is still served, so only the dismissal hid it.
  await page.evaluate((key) => localStorage.removeItem(key), DISMISSED_RUNS_KEY);
  await page.reload();
  await expect(pill).toBeVisible();
});

// focusRing reports whether the focused element shows its keyboard
// focus outline, and names the ancestor that clips it if one does. The
// walk stops at the fixed-position dock, above which nothing clips it.
async function focusRing(button: Locator): Promise<string> {
  return button.evaluate((el) => {
    if (!el.matches(":focus-visible")) return "not focus-visible";
    if (getComputedStyle(el).outlineStyle === "none") return "no outline";
    for (let a = el.parentElement; a; a = a.parentElement) {
      const s = getComputedStyle(a);
      if (s.overflowX !== "visible" || s.overflowY !== "visible") return `clipped by ${a.className}`;
      if (s.position === "fixed") break;
    }
    return "visible";
  });
}

test("tabbing between the pill's buttons shows each one's focus ring", async ({ page }) => {
  await page.goto(PR_FILES);
  const open = page.getByRole("button", { name: /Done · 0 suggestions/ });
  const dismiss = page.getByRole("button", { name: "Dismiss Claude review" });
  await expect(open).toBeVisible();

  await open.focus();
  await page.keyboard.press("Tab");
  await expect(dismiss).toBeFocused();
  expect(await focusRing(dismiss)).toBe("visible");

  await page.keyboard.press("Shift+Tab");
  await expect(open).toBeFocused();
  expect(await focusRing(open)).toBe("visible");
});
