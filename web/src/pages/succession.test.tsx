import { screen, within, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { RecoveryBanner } from "../Shell";
import { employee, person } from "../test/fixtures";
import { renderPage } from "../test/render";
import { fakeServer, principal } from "../test/server";
import { ApprovalsPage } from "./Approvals";
import { MailboxPage } from "./Mailbox";
import { RoleAcceptPage } from "./RoleAccept";
import { SettingsPage } from "./Settings";

afterEach(() => vi.unstubAllGlobals());

const sa = principal({ employee: { id: "sa", email: "sa@x.co", fullName: "Okan Admin", roles: ["SUPER_ADMIN"] } }) as never;
const is1 = principal({ employee: { id: "is1", email: "is@x.co", fullName: "Deniz Güvenlik", roles: ["INFOSEC"] } }) as never;
const hr = principal({ employee: { id: "hr", email: "hr@x.co", fullName: "Ayşe İK", roles: ["HR_MANAGER"] } }) as never;
const dialog = (name: string) => within(screen.getByRole("dialog", { name }));
const people = [employee({ id: "o1", fullName: "Can Owner", roles: [] }), employee({ id: "is1", fullName: "Deniz Güvenlik", roles: ["INFOSEC"] }),
  employee({ id: "sa", fullName: "Okan Admin", roles: ["SUPER_ADMIN"] })];
const nomination = (over: Record<string, unknown> = {}) => ({ id: "n1", nominee: person("o1", "Can Owner"), proposedBy: person("sa", "Okan Admin"), recovery: false,
  status: "PENDING", createdAt: "2026-10-28T09:00:00Z", expiresAt: "2026-11-04T09:00:00Z", version: 1, canAuthorize: false, ...over });
const health = (over: Record<string, unknown> = {}) => ({ activeSuperAdmins: 1, infosecMembers: 1, recoveryRequired: false, warning: true, ...over });
const base = { "GET /employees": { employees: people }, "GET /admin-groups": { groups: [] }, "GET /approvals": { approvals: [] }, "GET /tools": { tools: [] },
  "GET /admin/health": health() };

describe("Super Admin succession", () => {
  it("a Super Admin proposes a nominee and sees it pending", async () => {
    const calls = fakeServer({ ...base, "GET /super-admin-nominations": { nominations: [nomination()] },
      "POST /super-admin-nominations": () => ({ status: 201, body: nomination() }) });
    renderPage(<SettingsPage />, { session: sa });
    expect(await screen.findByText("Bekleyen Süper Yönetici önerileri")).toBeTruthy();
    expect(screen.getByText(/Öneren: Okan Admin/)).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Süper Yönetici öner" }));
    const d = dialog("Süper Yönetici öner");
    expect(d.getByText(/Bilgi Güvenliği üyesi yetkilendirecek/)).toBeTruthy();
    expect(d.queryByRole("option", { name: /Okan Admin/ })).toBeNull(); // never themself, never a Super Admin
    await userEvent.selectOptions(d.getByLabelText(/Aday/), "o1");
    await userEvent.click(d.getByRole("button", { name: "Öner" }));
    await waitFor(() => expect(calls.find((c) => c.method === "POST")?.body).toEqual({ nomineeId: "o1" }));
  });

  it("shows the Super Admin health on the Sistem tab", async () => {
    fakeServer({ ...base, "GET /super-admin-nominations": { nominations: [] }, "GET /admin/health": health({ activeSuperAdmins: 2, infosecMembers: 3, warning: false }) });
    renderPage(<SettingsPage />, { session: sa });
    await userEvent.click(await screen.findByRole("tab", { name: "Sistem" }));
    expect(await screen.findByText("Aktif Süper Yönetici")).toBeTruthy();
    expect(screen.getByText("2")).toBeTruthy();
    expect(screen.getByText("3")).toBeTruthy();
  });

  it("InfoSec authorizes a nomination from the approval inbox", async () => {
    const calls = fakeServer({ ...base, "GET /super-admin-nominations": { nominations: [nomination({ canAuthorize: true, version: 3 })] },
      "POST /super-admin-nominations/n1/authorize": nomination({ status: "AUTHORIZED" }) });
    renderPage(<ApprovalsPage />, { session: is1 });
    expect(await screen.findByText("Yetkilendirmenizi bekleyen Süper Yönetici önerileri")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Yetkilendir" }));
    await waitFor(() => expect(calls.find((c) => c.method === "POST")).toMatchObject({ path: "/super-admin-nominations/n1/authorize", body: { version: 3 } }));
  });

  it("hides nominations the caller may not decide", async () => {
    fakeServer({ ...base, "GET /super-admin-nominations": { nominations: [nomination()] } });
    renderPage(<ApprovalsPage />, { session: is1 });
    expect(await screen.findByText("Bekleyen onayınız yok")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Yetkilendir" })).toBeNull();
  });

  it("in recovery an HR Manager is told and appoints a successor, never themself", async () => {
    const calls = fakeServer({ ...base, "GET /employees": { employees: [...people, employee({ id: "hr", fullName: "Ayşe İK", roles: ["HR_MANAGER"] })] },
      "GET /admin/health": health({ activeSuperAdmins: 0, recoveryRequired: true }), "GET /super-admin-nominations": { nominations: [] },
      "POST /super-admin-nominations": () => ({ status: 201, body: nomination({ recovery: true, succession: true }) }) });
    renderPage(<RecoveryBanner />, { session: hr });
    expect(await screen.findByText("Aktif Süper Yönetici yok: kurtarma modu")).toBeTruthy();
    expect(screen.getByText(/bağımsız bir Bilgi Güvenliği üyesi yetkilendirir/)).toBeTruthy(); // R-2
    await userEvent.click(await screen.findByRole("button", { name: "Yeni Süper Yönetici ata" }));
    const d = dialog("Yeni Süper Yönetici ata");
    expect(within(d.getByLabelText(/Aday/)).queryByText(/Ayşe İK/)).toBeNull();
    await userEvent.selectOptions(d.getByLabelText(/Aday/), "o1");
    expect(d.getByText(/Bilgi Güvenliği üyesi veya aktif bir Süper Yönetici yetkilendirdikten sonra/)).toBeTruthy();
    await userEvent.click(d.getByRole("button", { name: "Yetkilendirmeye gönder" }));
    await waitFor(() => expect(calls.find((c) => c.method === "POST")?.body).toEqual({ nomineeId: "o1" }));
  });

  it("shows HR a successor awaiting authorization, with no link to reissue yet", async () => {
    fakeServer({ ...base, "GET /admin/health": health({ activeSuperAdmins: 0, recoveryRequired: true }),
      "GET /super-admin-nominations": { nominations: [nomination({ succession: true, recovery: true, proposedBy: person("hr", "Ayşe İK"), authorizedAt: null })] } });
    renderPage(<RecoveryBanner />, { session: hr });
    expect(await screen.findByText(/Can Owner · yetkilendirme bekleniyor/)).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Bağlantıyı yeniden gönder" })).toBeNull();
  });

  it("InfoSec sees HR's succession as an appointment to authorize", async () => {
    fakeServer({ ...base, "GET /super-admin-nominations": { nominations: [nomination({ succession: true, canAuthorize: true, proposedBy: person("hr", "Ayşe İK") })] } });
    renderPage(<ApprovalsPage />, { session: is1 });
    expect(await screen.findByText(/Öneren: Ayşe İK \(İK ataması\)/)).toBeTruthy();
  });

  it("shows HR a successor awaiting acceptance without the link", async () => {
    fakeServer({ ...base, "GET /admin/health": health({ activeSuperAdmins: 0, recoveryRequired: true }),
      "GET /super-admin-nominations": { nominations: [nomination({ succession: true, recovery: true, proposedBy: person("hr", "Ayşe İK"), expiresAt: "2099-01-01T09:00:00Z",
        authorizedAt: "2026-10-31T09:00:00Z" })] } });
    renderPage(<RecoveryBanner />, { session: hr });
    expect(await screen.findByText(/Can Owner · kabul bekleniyor/)).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Yeni Süper Yönetici ata" })).toBeNull();
    expect(screen.queryByText(/rol-kabul/)).toBeNull();
  });

  it("the nominee reveals their own role link in the mailbox and accepts it signed in", async () => {
    const mail = { id: "m1", kind: "ROLE_ACCEPTANCE", recipient: person("o1", "Can Owner"), requestId: null, subject: "Süper Yönetici rolü size atandı: kabulünüz bekleniyor",
      body: "Merhaba", createdAt: "2026-10-30T09:00:00Z", linkAvailable: true };
    const owner = principal({ employee: { id: "o1", email: "o@x.co", fullName: "Can Owner", roles: [] } }) as never;
    fakeServer({ ...base, "GET /email-outbox": { emails: [mail] }, "GET /email-outbox/m1/role-link": { token: "tok123" } });
    renderPage(<MailboxPage />, { session: owner });
    expect(screen.getByText(/Kimse kendi bağlantısını görüntüleyemez; Süper Yönetici veya Bilgi Güvenliği hesabının bağlantısını, sıfırlamayı yapmayan başka bir Süper Yönetici görür/)).toBeTruthy(); // R-1
    await userEvent.click(await screen.findByText(/kabulünüz bekleniyor/));
    await userEvent.click(screen.getByRole("button", { name: "Bağlantıyı göster" }));
    expect((await screen.findByRole("link", { name: /rol-kabul#tok123/ })).getAttribute("href")).toBe(`${window.location.origin}/rol-kabul#tok123`);
  });

  it("accepts the role with the link's token and asks to sign in again", async () => {
    window.history.replaceState(null, "", "/rol-kabul#tok123");
    const owner = principal({ employee: { id: "o1", email: "o@x.co", fullName: "Can Owner", roles: [] } }) as never;
    const calls = fakeServer({ ...base, "POST /super-admin-nominations/accept": { accepted: true } });
    renderPage(<RoleAcceptPage />, { session: owner });
    expect(window.location.hash).toBe(""); // the token leaves the address bar
    await userEvent.click(screen.getByRole("button", { name: "Rolü kabul et" }));
    await waitFor(() => expect(calls.find((c) => c.method === "POST")?.body).toEqual({ token: "tok123" }));
    expect(await screen.findByText("Süper Yönetici rolünüz etkinleşti")).toBeTruthy();
  });

  it("shows no banner outside recovery", async () => {
    const calls = fakeServer({ ...base });
    renderPage(<RecoveryBanner />, { session: hr });
    await waitFor(() => expect(calls.some((c) => c.path === "/admin/health")).toBe(true));
    expect(screen.queryByText(/kurtarma modu/)).toBeNull();
  });
});
