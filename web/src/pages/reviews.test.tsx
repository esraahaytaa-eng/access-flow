import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { person } from "../test/fixtures";
import { renderPage } from "../test/render";
import { fakeServer, principal } from "../test/server";
import { ExceptionsPage } from "./Exceptions";
import { ReviewsPage } from "./Reviews";
import { SettingsPage } from "./Settings";

afterEach(() => vi.unstubAllGlobals());

const owner = principal({ employee: { id: "o1", email: "o@x.co", fullName: "Can Owner", roles: [] } }) as never;
const sa = principal({ employee: { id: "sa", email: "sa@x.co", fullName: "Okan Admin", roles: ["SUPER_ADMIN"] } }) as never;
const infosec = principal({ employee: { id: "is1", email: "is@x.co", fullName: "Deniz Güvenlik", roles: ["INFOSEC"] } }) as never;
const dialog = (name: string) => within(screen.getByRole("dialog", { name }));

const campaign = { id: "c1", kind: "MANUAL", quarter: null, startedAt: "2026-10-20T09:00:00Z", startedBy: person("sa", "Okan Admin"), remindedAt: null, escalatedAt: null, closedAt: null };
const reviewItem = (over: Record<string, unknown> = {}) => ({
  id: "ri1", campaignId: "c1", campaignStartedAt: campaign.startedAt, grantId: "g1",
  employee: { id: "e2", fullName: "Deniz Kara", jobTitle: "Analist", status: "ACTIVE" }, toolId: "t1", tool: "Jira", accessLevelId: "l1", level: "Kullanıcı",
  grantStatus: "ACTIVE", expiresOn: null, effectiveFrom: "2026-09-01T09:00:00Z", reviewerId: "o1", reviewerName: "Can Owner", routingNote: null,
  requiresInfosec: false, flags: [], decision: null, decidedBy: null, decidedAt: null, decisionReason: null, late: false, findingAt: null,
  closedReason: null, closedAt: null, version: 3, ...over,
});
const base = { "GET /employees": { employees: [] }, "GET /approvals": { approvals: [] }, "GET /tools": { tools: [] } };
const noExceptions = { failedOperations: [], unroutableSteps: [], overlaps: [], suspensionGaps: [], securityRevocations: [], staleGrants: [], reviewFindings: [], orphanAccounts: [] };

describe("access reviews", () => {
  it("keeps an item with its version and bulk-keeps only eligible items", async () => {
    const items = [reviewItem(), reviewItem({ id: "ri2", grantId: "g2", level: "Yönetici", requiresInfosec: true }), reviewItem({ id: "ri3", grantId: "g3", tool: "GitHub", flags: ["IMPORTED_BASELINE"] }), reviewItem({ id: "ri4", grantId: "g4", tool: "Figma" })];
    const calls = fakeServer({ ...base, "GET /reviews": { campaign, items }, "POST /review-items/ri1/decide": reviewItem({ decision: "KEEP" }), "POST /review-items/bulk-keep": { kept: 1, skipped: 0 } });
    renderPage(<ReviewsPage />, { session: owner });
    expect(await screen.findByText("İçe aktarılan")).toBeTruthy();
    const rows = screen.getAllByRole("row");
    await userEvent.click(within(rows[1]).getByRole("button", { name: "Koru" }));
    await waitFor(() => expect(calls.find((c) => c.path === "/review-items/ri1/decide")?.body).toEqual({ decision: "KEEP", reason: "", version: 3 }));

    // Only ri1 and ri4 may be kept in bulk: ri2's level needs InfoSec, ri3 is flagged.
    expect(screen.getAllByRole("checkbox", { name: /^Deniz Kara · / })).toHaveLength(2);
    await userEvent.click(screen.getByRole("checkbox", { name: "Uygun olanların tümünü seç" }));
    await userEvent.click(screen.getByRole("button", { name: "Seçilenleri koru" }));
    await waitFor(() => expect(calls.find((c) => c.path === "/review-items/bulk-keep")?.body).toEqual({ items: [{ id: "ri1", version: 3 }, { id: "ri4", version: 3 }] }));
  });

  it("revokes with a reason; a Super Admin is offered Revoke only", async () => {
    const calls = fakeServer({ ...base, "GET /reviews": { campaign, items: [reviewItem()] }, "POST /review-items/ri1/decide": reviewItem({ decision: "REVOKE" }) });
    renderPage(<ReviewsPage />, { session: sa });
    expect(await screen.findByText(/yalnızca “Kaldırılsın”/)).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Koru" })).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "Kaldır" }));
    await userEvent.type(dialog("Erişim kaldırılsın mı?").getByRole("textbox"), "Proje bitti");
    await userEvent.click(dialog("Erişim kaldırılsın mı?").getByRole("button", { name: "Kaldırılsın" }));
    await waitFor(() => expect(calls.find((c) => c.method === "POST")?.body).toEqual({ decision: "REVOKE", reason: "Proje bitti", version: 3 }));
  });

  it("shows when no campaign is active", async () => {
    fakeServer({ ...base, "GET /reviews": { campaign: null, items: [] } });
    renderPage(<ReviewsPage />, { session: owner });
    expect(await screen.findByText("Aktif inceleme kampanyası yok")).toBeTruthy();
  });
});

describe("settings: reviews and imports", () => {
  it("starts a campaign manually", async () => {
    const calls = fakeServer({ ...base, "GET /reviews": { campaign: null, items: [] }, "POST /reviews/campaigns": () => ({ status: 201, body: campaign }) });
    renderPage(<SettingsPage />, { session: sa });
    await userEvent.click(screen.getByRole("tab", { name: "İnceleme takvimi" }));
    await userEvent.click(await screen.findByRole("button", { name: "Kampanyayı şimdi başlat" }));
    await userEvent.click(dialog("Yeni inceleme kampanyası başlatılsın mı?").getByRole("button", { name: "Başlat" }));
    await waitFor(() => expect(calls.some((c) => c.method === "POST" && c.path === "/reviews/campaigns")).toBe(true));
  });

  it("imports from the mock and from CSV, and shows the report", async () => {
    const report = { id: "imp1", source: "CSV", fileName: "yapistirilan_veri.csv", importedBy: person("sa", "Okan Admin"), importedAt: "2026-10-29T09:00:00Z",
      imported: 2, orphans: 1, terminated: 0, skipped: 0, rejected: [{ line: 3, account: "x", tool: "Sketch", level: "Editor", reason: "unknown_tool" }] };
    const calls = fakeServer({ ...base, "GET /imports": { imports: [] }, "POST /imports/mock": () => ({ status: 201, body: { ...report, id: "imp0", source: "MOCK", fileName: null, rejected: [] } }),
      "POST /imports/csv": () => ({ status: 201, body: report }) });
    renderPage(<SettingsPage />, { session: sa });
    await userEvent.click(screen.getByRole("tab", { name: "İçe aktarma" }));
    await userEvent.click(await screen.findByText("Gelişmiş içe aktarma seçenekleri"));
    await userEvent.click(screen.getByRole("button", { name: "Araç servisinden içe aktar" }));
    await waitFor(() => expect(calls.some((c) => c.path === "/imports/mock")).toBe(true));
    await userEvent.type(screen.getByRole("textbox", { name: "İçerik" }), "eposta;arac;seviye;hesap{enter}a@x.co;Figma;Editor;");
    await userEvent.click(screen.getByRole("button", { name: "İçe aktar" }));
    await waitFor(() => expect(calls.find((c) => c.path === "/imports/csv")?.body).toEqual({ fileName: "yapistirilan_veri.csv", content: "eposta;arac;seviye;hesap\na@x.co;Figma;Editor;" }));
    expect(await screen.findByText("Satır 3: Sketch · Editor — bilinmeyen araç")).toBeTruthy();
  });
});

describe("settings: Excel workbook import", () => {
  const plan = (over: Record<string, unknown> = {}) => ({
    summary: { employeesNew: 2, employeesExisting: 1, toolsNew: 1, toolsExisting: 0, levelsNew: 2, levelsExisting: 0, accessNew: 3, accessExisting: 1 },
    owners: [{ tool: "Notion", ownerEmail: "can@x.co" }], problems: [], warnings: [], ...over,
  });
  const report = { id: "imp2", source: "WORKBOOK", fileName: "ekip.xlsx", importedBy: person("sa", "Okan Admin"), importedAt: "2026-10-29T09:00:00Z",
    imported: 3, orphans: 0, terminated: 0, skipped: 1, rejected: [], created: { employees: 2, tools: 1, levels: 2 } };
  const file = new File(["xlsx"], "ekip.xlsx", { type: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" });

  it("leads with the workbook and keeps the tool read and the CSV under collapsed advanced options", async () => {
    fakeServer({ ...base, "GET /imports": { imports: [] } });
    renderPage(<SettingsPage />, { session: sa });
    await userEvent.click(screen.getByRole("tab", { name: "İçe aktarma" }));
    const summary = await screen.findByText("Gelişmiş içe aktarma seçenekleri");
    const advanced = summary.closest("details")!;
    expect(advanced.open).toBe(false);
    expect(within(advanced).getByRole("button", { name: "Araç servisinden içe aktar" })).toBeTruthy();
    expect(within(advanced).getByLabelText("CSV dosyası")).toBeTruthy();
    expect(advanced.contains(screen.getByLabelText("Excel dosyası"))).toBe(false);
    await userEvent.click(summary);
    expect(advanced.open).toBe(true);
  });

  it("uses Turkish file selectors that show the chosen file", async () => {
    fakeServer({ ...base, "GET /imports": { imports: [] } });
    renderPage(<SettingsPage />, { session: sa });
    await userEvent.click(screen.getByRole("tab", { name: "İçe aktarma" }));
    expect((await screen.findAllByText("Dosya seçilmedi")).length).toBe(2);
    expect(screen.getAllByText("Dosya seç").length).toBe(2);
    await userEvent.upload(screen.getByLabelText("Excel dosyası"), file);
    expect(screen.getByText("ekip.xlsx")).toBeTruthy();
    expect(screen.getAllByText("Dosya seçilmedi").length).toBe(1);
  });

  it("previews a file and imports it only after a clean preview", async () => {
    const calls = fakeServer({ ...base, "GET /imports": { imports: [] },
      "POST /imports/workbook/preview": () => ({ body: plan({ warnings: [{ sheet: "Employees", row: 4, column: "B", value: "Ali V.", reason: "employee_differs", expected: "Ali Veli" }] }) }),
      "POST /imports/workbook?fileName=ekip.xlsx": () => ({ status: 201, body: { plan: plan(), report } }) });
    renderPage(<SettingsPage />, { session: sa });
    await userEvent.click(screen.getByRole("tab", { name: "İçe aktarma" }));
    expect((await screen.findByRole("button", { name: "Excel’i içe aktar" }) as HTMLButtonElement).disabled).toBe(true);
    await userEvent.upload(screen.getByLabelText("Excel dosyası"), file);
    await userEvent.click(screen.getByRole("button", { name: "Önizle" }));
    expect(await screen.findByText("2 yeni çalışan")).toBeTruthy();
    expect(screen.getByText("Notion → can@x.co")).toBeTruthy();
    expect(screen.getByText("Kayıtlı bilgiden farklı; mevcut kayıt değiştirilmez")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Excel’i içe aktar" }));
    await waitFor(() => expect(calls.find((c) => c.path === "/imports/workbook?fileName=ekip.xlsx")?.body).toEqual({ file: "ekip.xlsx" }));
    expect(await screen.findByText("2 çalışan · 1 araç · 2 seviye oluşturuldu")).toBeTruthy();
  });

  it("lists every problem with its sheet, row and expected value and blocks the import", async () => {
    fakeServer({ ...base, "GET /imports": { imports: [] },
      "POST /imports/workbook/preview": () => ({ body: plan({ problems: [{ sheet: "Employees", row: 3, column: "C", value: "engineering", reason: "unknown_department", expected: "Engineering" }] }) }) });
    renderPage(<SettingsPage />, { session: sa });
    await userEvent.click(screen.getByRole("tab", { name: "İçe aktarma" }));
    await userEvent.upload(await screen.findByLabelText("Excel dosyası"), file);
    await userEvent.click(screen.getByRole("button", { name: "Önizle" }));
    const row = (await screen.findByText("Tanımlı olmayan departman")).closest("tr")!;
    expect(within(row).getByText("Employees")).toBeTruthy();
    expect(within(row).getByText("3")).toBeTruthy();
    expect(within(row).getByText("Engineering")).toBeTruthy();
    expect((screen.getByRole("button", { name: "Excel’i içe aktar" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("downloads the template", async () => {
    const save = vi.fn();
    URL.createObjectURL = save.mockReturnValue("blob:x");
    URL.revokeObjectURL = vi.fn();
    const calls = fakeServer({ ...base, "GET /imports": { imports: [] }, "GET /imports/workbook/template": {} });
    renderPage(<SettingsPage />, { session: sa });
    await userEvent.click(screen.getByRole("tab", { name: "İçe aktarma" }));
    await userEvent.click(await screen.findByRole("button", { name: "Excel şablonunu indir" }));
    await waitFor(() => expect(save).toHaveBeenCalled());
    expect(calls.some((c) => c.path === "/imports/workbook/template")).toBe(true);
  });
});

describe("exceptions: findings and orphans", () => {
  const orphan = { id: "g9", toolId: "t2", tool: "GitHub", accessLevelId: "l9", level: "Developer", method: "API", account: "ghost@example.com", status: "ACTIVE",
    source: "MOCK", fileName: null, importedAt: "2026-10-29T09:00:00Z", version: 1 };

  it("lets InfoSec dismiss an orphan with a reason", async () => {
    const calls = fakeServer({ ...base, "GET /exceptions": { ...noExceptions, orphanAccounts: [orphan], reviewFindings: [reviewItem({ findingAt: "2026-11-10T09:00:00Z" })] },
      "POST /orphans/g9/dismiss": orphan });
    renderPage(<ExceptionsPage />, { session: infosec });
    expect(await screen.findByText("ghost@example.com")).toBeTruthy();
    expect(screen.getByText("Açık inceleme bulgusu")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Bulguyu kapat" }));
    await userEvent.type(dialog("Bulgu kapatılsın mı?").getByRole("textbox"), "Kiosk hesabı, bilerek tutuluyor");
    await userEvent.click(dialog("Bulgu kapatılsın mı?").getByRole("button", { name: "Bulguyu kapat" }));
    await waitFor(() => expect(calls.find((c) => c.method === "POST")?.body).toEqual({ reason: "Kiosk hesabı, bilerek tutuluyor", version: 1 }));
  });
});
