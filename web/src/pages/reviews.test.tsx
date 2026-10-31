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
/** GET /reviews: one page of the server's view, with counts over everything (PERF-004). */
const reviewPage = (c: Record<string, unknown> | null, items: ReturnType<typeof reviewItem>[], over: Record<string, unknown> = {}) => ({
  campaign: c, items, view: "mine", total: items.length, page: 1, pageSize: 100,
  counts: { mine: items.length, esc: 0, all: items.length, findings: 0, done: items.filter((i) => i.decision || i.closedReason).length, waiting: items.filter((i) => !i.decision).length }, ...over,
});
const base = { "GET /employees": { employees: [] }, "GET /approvals": { approvals: [] }, "GET /tools": { tools: [] } };
const noExceptions = { failedOperations: [], unroutableSteps: [], overlaps: [], suspensionGaps: [], securityRevocations: [], staleGrants: [], reviewFindings: [], reviewFindingsTotal: 0, orphanAccounts: [], totals: { failedOperations: 0, unroutableSteps: 0, overlaps: 0, suspensionGaps: 0, securityRevocations: 0, staleGrants: 0, orphanAccounts: 0 } };

describe("access reviews", () => {
  it("keeps an item with its version and bulk-keeps only eligible items", async () => {
    const items = [reviewItem(), reviewItem({ id: "ri2", grantId: "g2", level: "Yönetici", requiresInfosec: true }), reviewItem({ id: "ri3", grantId: "g3", tool: "GitHub", flags: ["IMPORTED_BASELINE"] }), reviewItem({ id: "ri4", grantId: "g4", tool: "Figma" })];
    const calls = fakeServer({ ...base, "GET /reviews": reviewPage(campaign, items), "POST /review-items/ri1/decide": reviewItem({ decision: "KEEP" }), "POST /review-items/bulk-keep": { kept: 1, skipped: 0 } });
    renderPage(<ReviewsPage />, { session: owner });
    expect(await screen.findByText("İçe aktarılan")).toBeTruthy();
    const rows = screen.getAllByRole("row");
    await userEvent.click(within(rows[1]).getByRole("button", { name: "Erişim devam etsin" }));
    await waitFor(() => expect(calls.find((c) => c.path === "/review-items/ri1/decide")?.body).toEqual({ decision: "KEEP", reason: "", version: 3 }));

    // Only ri1 and ri4 may be kept in bulk: ri2's level needs InfoSec, ri3 is flagged.
    expect(screen.getAllByRole("checkbox", { name: /^Deniz Kara · / })).toHaveLength(2);
    await userEvent.click(screen.getByRole("checkbox", { name: "Bu sayfada uygun olanların tümünü seç" }));
    await userEvent.click(screen.getByRole("button", { name: "Seçilenlerin erişimi devam etsin" }));
    await waitFor(() => expect(calls.find((c) => c.path === "/review-items/bulk-keep")?.body).toEqual({ items: [{ id: "ri1", version: 3 }, { id: "ri4", version: 3 }] }));
  });

  it("revokes with a reason; a Super Admin is offered Revoke only", async () => {
    const calls = fakeServer({ ...base, "GET /reviews": reviewPage(campaign, [reviewItem()]), "POST /review-items/ri1/decide": reviewItem({ decision: "REVOKE" }) });
    renderPage(<ReviewsPage />, { session: sa });
    expect(await screen.findByText(/yalnızca “Erişimi kaldır”/)).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Erişim devam etsin" })).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "Erişimi kaldır" }));
    await userEvent.type(dialog("Erişim kaldırılsın mı?").getByRole("textbox"), "Proje bitti");
    await userEvent.click(dialog("Erişim kaldırılsın mı?").getByRole("button", { name: "Erişimi kaldır" }));
    await waitFor(() => expect(calls.find((c) => c.method === "POST")?.body).toEqual({ decision: "REVOKE", reason: "Proje bitti", version: 3 }));
  });

  it("titles the campaign by year and quarter and keeps the day timeline", async () => {
    fakeServer({ ...base, "GET /reviews": reviewPage(campaign, [reviewItem()]) });
    renderPage(<ReviewsPage />, { session: owner });
    expect(await screen.findByText("Erişim Denetimi — 2026 4. Çeyrek")).toBeTruthy();
    expect(screen.getByRole("heading", { name: "Erişim Kontrolü" })).toBeTruthy();
    expect(screen.getByText(/Gün 21/)).toBeTruthy();
    expect(screen.queryByText(/Ek inceleme/)).toBeNull();
  });

  it("names a scheduled campaign's quarter from its key", async () => {
    fakeServer({ ...base, "GET /reviews": reviewPage({ ...campaign, kind: "QUARTERLY", quarter: "2027-Q1" }, [reviewItem()]) });
    renderPage(<ReviewsPage />, { session: owner });
    expect(await screen.findByText("Erişim Denetimi — 2027 1. Çeyrek")).toBeTruthy();
  });

  it("shows a leaver's earlier decision as history with the access's removal state, and offers no Keep", async () => {
    const leaver = { id: "e9", fullName: "Deniz Kara", jobTitle: "Analist", status: "TERMINATED" };
    const items = [
      reviewItem({ employee: leaver, decision: "KEEP", decidedBy: person("o2", "Can Öztürk"), decidedAt: "2026-10-29T21:08:59Z", grantStatus: "REVOKED" }),
      reviewItem({ id: "ri2", grantId: "g2", tool: "GitHub", employee: leaver, grantStatus: "REVOCATION_REQUESTED" }),
      reviewItem({ id: "ri3", grantId: "g3", tool: "Figma", employee: leaver, grantStatus: "REVOCATION_FAILED" }),
    ];
    fakeServer({ ...base, "GET /reviews": reviewPage(campaign, items) });
    renderPage(<ReviewsPage />, { session: owner });
    await userEvent.click(await screen.findByRole("tab", { name: /Tüm kalemlerim/ }));
    await screen.findByText("Kaldırma bekleniyor");
    const rows = screen.getAllByRole("row");
    const jira = within(rows.find((r) => r.textContent?.includes("Jira"))!);
    expect(jira.getByText("Erişimin devamı onaylandı")).toBeTruthy();
    expect(jira.getByText(/Can Öztürk/)).toBeTruthy();
    expect(jira.getByText("Kaldırıldı")).toBeTruthy();
    expect(screen.getByText("Kaldırma bekleniyor")).toBeTruthy();
    expect(screen.getByText("Kaldırma başarısız")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Erişim devam etsin" })).toBeNull();
    expect(screen.queryByRole("checkbox", { name: /Deniz Kara · / })).toBeNull();
  });

  it("pages through a large campaign on the server and searches all of it (PERF-004)", async () => {
    const calls = fakeServer({ ...base, "GET /reviews": reviewPage(campaign, [reviewItem()], { total: 230, counts: { mine: 230, esc: 0, all: 230, findings: 0, done: 0, waiting: 230 } }) });
    renderPage(<ReviewsPage />, { session: owner });
    expect(await screen.findByText("1–100 / 230")).toBeTruthy();
    expect(screen.getByRole("tab", { name: /İncelemem gerekenler/ }).textContent).toContain("230");
    await userEvent.click(screen.getByRole("button", { name: "Sonraki" }));
    await waitFor(() => expect(calls.some((c) => c.path === "/reviews?page=2")).toBe(true));
    await userEvent.type(screen.getByRole("searchbox"), "Kara");
    await waitFor(() => expect(calls.some((c) => c.path === "/reviews?q=Kara&page=1")).toBe(true)); // a new search starts at page 1
  });

  it("opens the findings view from the Exceptions link", async () => {
    const calls = fakeServer({ ...base, "GET /reviews": reviewPage(null, [reviewItem({ findingAt: "2026-11-10T09:00:00Z" })], { view: "findings" }) });
    renderPage(<ReviewsPage />, { session: infosec, path: "/reviews?view=findings" });
    await waitFor(() => expect(calls.some((c) => c.path.startsWith("/reviews?view=findings"))).toBe(true));
    expect(await screen.findByRole("tab", { name: /Açık bulgular/ })).toBeTruthy();
  });

  it("shows when no campaign is active", async () => {
    fakeServer({ ...base, "GET /reviews": reviewPage(null, []) });
    renderPage(<ReviewsPage />, { session: owner });
    expect(await screen.findByText("Aktif erişim denetimi yok")).toBeTruthy();
  });
});

describe("settings: reviews and imports", () => {
  it("starts a campaign manually", async () => {
    const calls = fakeServer({ ...base, "GET /reviews": reviewPage(null, []), "POST /reviews/campaigns": () => ({ status: 201, body: campaign }) });
    renderPage(<SettingsPage />, { session: sa });
    await userEvent.click(screen.getByRole("tab", { name: "Denetim takvimi" }));
    expect(await screen.findByText("Üç aylık erişim denetimi takvimi")).toBeTruthy();
    expect(screen.getByText("Yanıtlanmamış erişimler için güvenlik bulgusu oluşturulur ve Bilgi Güvenliği tarafından takip edilir. Otomatik erişim kaldırılmaz.")).toBeTruthy();
    expect(document.body.textContent).not.toMatch(/kampanya/i);
    await userEvent.click(await screen.findByRole("button", { name: "Erişim denetimi başlat" }));
    await userEvent.click(dialog("Yeni erişim denetimi başlatılsın mı?").getByRole("button", { name: "Başlat" }));
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

  it("groups orphan and departed-employee accounts and says why and where each came from", async () => {
    const departed = { id: "op7", grantId: "g7", type: "REVOKE", employee: person("e7", "Eski Çalışan"), tool: "Jira", level: "Kullanıcı", status: "PENDING",
      attempts: 0, lastError: null, failedAt: null, version: 1, createdAt: "2026-10-29T09:00:00Z" };
    const accounts = [
      { ...orphan, id: "o1", account: "ghost@ornekfirma.co" },
      { ...orphan, id: "o2", account: "designer@ajans.com", source: "CSV", fileName: "figma.csv" },
      { ...orphan, id: "o3", account: "svc-build", source: "WORKBOOK", fileName: "onboarding.xlsx" },
      { ...orphan, id: "o4", account: "John@ORNEKFIRMA.CO" }, // case does not matter (no Turkish dotless ı)
    ];
    fakeServer({ ...base, "GET /employees": { employees: [{ id: "e1", email: "elif@ornekfirma.co", fullName: "Elif", department: null, jobTitle: null, managerId: null }] },
      "GET /exceptions": { ...noExceptions, orphanAccounts: accounts, securityRevocations: [departed] } });
    renderPage(<ExceptionsPage />, { session: infosec });
    await userEvent.click(await screen.findByRole("button", { name: "Sahipsiz ve Şüpheli Hesaplar (5)" }));
    const card = (text: string) => within(screen.getByText(text).closest(".card") as HTMLElement);
    expect(card("Eski Çalışan").getByText("Ayrılan çalışanın kalan erişimi")).toBeTruthy();
    expect(card("ghost@ornekfirma.co").getByText("Çalışan kaydı olmayan şirket hesabı")).toBeTruthy();
    expect(card("ghost@ornekfirma.co").getByText(/Araç servisi okuması/)).toBeTruthy();
    expect(card("designer@ajans.com").getByText("Harici olabilir (tedarikçi veya ajans)")).toBeTruthy();
    expect(card("designer@ajans.com").getByText(/CSV içe aktarımı · figma.csv/)).toBeTruthy();
    expect(card("svc-build").getByText("Manuel inceleme gerekli")).toBeTruthy();
    expect(card("John@ORNEKFIRMA.CO").getByText("Çalışan kaydı olmayan şirket hesabı")).toBeTruthy();
    expect(card("svc-build").getByText(/Excel çalışma kitabı · onboarding.xlsx/)).toBeTruthy();
    expect(screen.queryByText(/Hesap tutarsızlıkları/)).toBeNull();
  });

  it("counts every open finding and links to all of them when only the first page is listed (PERF-004)", async () => {
    fakeServer({ ...base, "GET /exceptions": { ...noExceptions, reviewFindings: [reviewItem({ findingAt: "2026-11-10T09:00:00Z" })], reviewFindingsTotal: 230 } });
    renderPage(<ExceptionsPage />, { session: infosec });
    expect(await screen.findByRole("button", { name: "Tümü (230)" })).toBeTruthy();
    expect(screen.getByRole("link", { name: /Açık bulgular/ }).getAttribute("href")).toBe("/reviews?view=findings");
  });

  it("lets InfoSec dismiss an orphan with a reason", async () => {
    const calls = fakeServer({ ...base, "GET /exceptions": { ...noExceptions, orphanAccounts: [orphan], reviewFindings: [reviewItem({ findingAt: "2026-11-10T09:00:00Z" })], reviewFindingsTotal: 1 },
      "POST /orphans/g9/dismiss": orphan });
    renderPage(<ExceptionsPage />, { session: infosec });
    expect(await screen.findByText("ghost@example.com")).toBeTruthy();
    expect(screen.getByText("Açık inceleme bulgusu")).toBeTruthy();
    expect(screen.getByText("Onay ve denetim (1)")).toBeTruthy(); // findings are not filed under approvals alone (BUG-B-02)
    await userEvent.click(screen.getByRole("button", { name: "Bulguyu kapat" }));
    await userEvent.type(dialog("Bulgu kapatılsın mı?").getByRole("textbox"), "Kiosk hesabı, bilerek tutuluyor");
    await userEvent.click(dialog("Bulgu kapatılsın mı?").getByRole("button", { name: "Bulguyu kapat" }));
    await waitFor(() => expect(calls.find((c) => c.method === "POST")?.body).toEqual({ reason: "Kiosk hesabı, bilerek tutuluyor", version: 1 }));
  });
});
