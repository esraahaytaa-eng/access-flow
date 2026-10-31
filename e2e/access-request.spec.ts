import { expect, test, type Browser, type Page } from "@playwright/test";
import { randomBytes } from "node:crypto";

// One connected journey through the real UI, on the fresh demo stack ./e2e.sh starts: HR hires an
// engineer, a Super Admin passes on the activation link, the engineer sets a password and asks
// for AWS Console / ReadOnly for 7 days, the manager and the Tool Owner approve, the runner grants
// it through the Mock Tool Service and verifies it, and the Auditor reads who approved.
// Passwords, the activation link and cookies are never printed: assertions on them are booleans.

const demoPasswords = Object.fromEntries((process.env.DEMO_PASSWORDS ?? "").split(",").map((p) => p.split("=")));
const demo = (key: string, email: string) => ({ email, password: demoPasswords[key] ?? "" });
const hr = demo("hr", "ayse.demir@accessflow.local");
const admin = demo("admin", "admin@accessflow.local");
const manager = demo("manager", "burak.sahin@accessflow.local");
const owner = demo("cloud", "emre.kaya@accessflow.local");
const auditor = demo("auditor", "gizem.arslan@accessflow.local");
const engineer = { name: "Ada Deneme", email: "ada.deneme@accessflow.local", password: randomBytes(18).toString("base64url") };

/** Signs in through the login form in a browser context of its own (one per person). */
async function signIn(browser: Browser, who: { email: string; password: string }): Promise<Page> {
  const page = await (await browser.newContext()).newPage();
  await page.goto("/");
  await page.getByLabel("E-posta", { exact: true }).fill(who.email);
  await page.getByLabel("Parola", { exact: true }).fill(who.password);
  await page.getByRole("button", { name: "Giriş yap", exact: true }).click();
  await expect(page.getByRole("complementary", { name: "Ana gezinme" })).toBeVisible();
  return page;
}

/** Opens the engineer's pending step in the Approval Inbox and approves it; the inbox then no longer lists it. */
async function approve(page: Page) {
  await page.goto("/approvals");
  await page.getByRole("row", { name: new RegExp(engineer.name) }).click();
  await page.getByRole("dialog").getByRole("button", { name: "Onayla" }).click();
  await expect(page.getByRole("row", { name: new RegExp(engineer.name) })).toHaveCount(0);
}

test("HR hires an engineer who gets AWS Console / ReadOnly approved, provisioned, verified and evidenced", async ({ browser }) => {
  expect(Object.keys(demoPasswords).length > 0, "DEMO_PASSWORDS is set").toBe(true);

  await test.step("HR Manager creates an Engineering employee reporting to Burak Şahin", async () => {
    const page = await signIn(browser, hr);
    await page.goto("/employees");
    await page.getByRole("button", { name: "Yeni çalışan" }).click();
    const form = page.getByRole("dialog", { name: "Yeni çalışan" });
    await form.getByLabel("Ad soyad").fill(engineer.name);
    await form.getByLabel("E-posta").fill(engineer.email);
    await form.getByLabel("Unvan").selectOption({ label: "Backend Developer" });
    await form.getByLabel("Departman").selectOption({ label: "Engineering" });
    await form.getByLabel("Yönetici").selectOption({ label: "Burak Şahin — Engineering Manager" });
    await form.getByRole("button", { name: "Çalışanı oluştur" }).click();
    await expect(page.getByText(`${engineer.name} oluşturuldu`)).toBeVisible();
    await page.context().close();
  });

  let link = "";
  await test.step("Super Admin reveals the activation link in the Mock Email Outbox", async () => {
    const page = await signIn(browser, admin);
    await page.goto("/mailbox");
    await page.getByRole("button", { name: new RegExp(`Alıcı: ${engineer.name}`) }).click();
    const mail = page.getByRole("dialog");
    await mail.getByRole("button", { name: "Bağlantıyı göster" }).click();
    // Located by attribute and read without an assertion on it, so no log line carries the token.
    const anchor = mail.locator('a[href*="/sifre-belirle#"]');
    await anchor.waitFor();
    link = (await anchor.getAttribute("href")) ?? "";
    expect(link.includes("/sifre-belirle#"), "an activation link was revealed").toBe(true);
    await page.context().close();
  });

  let employee!: Page;
  await test.step("The employee activates the account and signs in", async () => {
    const page = await (await browser.newContext()).newPage();
    await page.goto("/");
    // Navigate from the page, so a failure message never contains the URL with its token.
    await page.evaluate((href) => { window.location.href = href; }, link);
    await page.getByLabel("Yeni parola", { exact: true }).fill(engineer.password);
    await page.getByLabel("Yeni parola (tekrar)").fill(engineer.password);
    await page.getByRole("button", { name: "Parolamı belirle" }).click();
    await expect(page.getByRole("link", { name: "Giriş yap" })).toBeVisible();
    await page.context().close();
    employee = await signIn(browser, engineer);
  });

  await test.step("The employee requests AWS Console / ReadOnly for 7 days", async () => {
    await employee.goto("/request-new");
    await employee.getByRole("combobox", { name: "Araç" }).fill("AWS");
    await employee.getByRole("option", { name: "AWS Console" }).click();
    await employee.getByRole("radio", { name: /ReadOnly/ }).click();
    await employee.getByRole("button", { name: /Süreli/ }).click();
    await employee.getByRole("button", { name: "7 gün" }).click();
    await employee.getByLabel("Bu erişime neden ihtiyaç var?").fill("Üretim ortamındaki servis günlüklerini incelemek için.");
    await employee.getByRole("button", { name: "Talebi gönder" }).click();
    await expect(employee).toHaveURL(/\/requests\/[0-9a-f-]+$/);
    await expect(employee.getByText("Onay bekliyor").first()).toBeVisible();
  });

  await test.step("Burak Şahin approves the Manager step", async () => {
    const page = await signIn(browser, manager);
    await approve(page);
    await page.context().close();
  });

  await test.step("Emre Kaya approves the Tool Owner step", async () => {
    const page = await signIn(browser, owner);
    await approve(page);
    await page.context().close();
  });

  await test.step("The runner provisions and verifies it; My Access shows it active after a reload", async () => {
    const row = employee.getByRole("row", { name: /AWS Console/ });
    // The operation runner works in the background: reload until the stored state is final.
    await expect(async () => {
      await employee.goto("/access");
      await expect(row).toContainText("Aktif", { timeout: 2_000 });
    }).toPass({ timeout: 60_000 });
    await expect(row).toContainText("ReadOnly");
    await expect(row).toContainText("Doğrulandı"); // read back from the Mock Tool Service, not self-reported
    await expect(row).toContainText("7 gün kaldı");
    await employee.context().close();
  });

  await test.step("The Auditor reads the approval evidence with the actual decision makers", async () => {
    const page = await signIn(browser, auditor);
    await page.goto("/audit?tab=evidence");
    await page.getByPlaceholder("Talep no, çalışan veya araç ara").fill(engineer.name);
    const row = page.getByRole("row", { name: new RegExp(engineer.name) });
    await expect(row).toHaveCount(1);
    await expect(row).toContainText("AWS Console");
    await expect(row).toContainText("Yönetici: Burak Şahin → Araç sahibi: Emre Kaya");
    await page.context().close();
  });
});
