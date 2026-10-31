import { expect, test } from "@playwright/test";

// Regression: over plain HTTP on a host other than localhost (a hostname or an IP) the browser is
// not a secure context and offers no crypto.randomUUID; the request and direct-grant forms went
// blank. accessflow.test is mapped to the app only inside this browser.
test.use({
  baseURL: "http://accessflow.test:8080",
  launchOptions: { args: ["--host-resolver-rules=MAP accessflow.test 127.0.0.1"] },
});

const password = Object.fromEntries((process.env.DEMO_PASSWORDS ?? "").split(",").map((p) => p.split("="))).admin ?? "";

test("the request and direct-grant forms render over plain HTTP on a non-localhost host name", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/");
  expect(await page.evaluate(() => window.isSecureContext), "plain HTTP on a hostname is not a secure context").toBe(false);
  await page.getByLabel("E-posta", { exact: true }).fill("admin@accessflow.local");
  await page.getByLabel("Parola", { exact: true }).fill(password);
  await page.getByRole("button", { name: "Giriş yap", exact: true }).click();
  await expect(page.getByRole("complementary", { name: "Ana gezinme" })).toBeVisible();

  await page.goto("/request-new");
  await expect(page.getByRole("combobox", { name: "Araç" })).toBeVisible();
  await page.goto("/direct");
  await expect(page.getByRole("button", { name: "Doğrudan erişim ver" })).toBeVisible();
  expect(errors).toEqual([]);
});
