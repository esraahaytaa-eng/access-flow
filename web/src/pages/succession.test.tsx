import { screen, within, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { RecoveryBanner } from "../Shell";
import { employee, person } from "../test/fixtures";
import { renderPage } from "../test/render";
import { fakeServer, principal } from "../test/server";
import { ApprovalsPage } from "./Approvals";
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

  it("in recovery an HR Manager is told and may nominate, themself included", async () => {
    const calls = fakeServer({ ...base, "GET /admin/health": health({ activeSuperAdmins: 0, recoveryRequired: true }),
      "POST /super-admin-nominations": () => ({ status: 201, body: nomination({ recovery: true }) }) });
    renderPage(<RecoveryBanner />, { session: hr });
    expect(await screen.findByText("Aktif Süper Yönetici yok: kurtarma modu")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Süper Yönetici öner" }));
    const d = dialog("Süper Yönetici öner");
    await userEvent.selectOptions(d.getByLabelText(/Aday/), "o1");
    await userEvent.click(d.getByRole("button", { name: "Öner" }));
    await waitFor(() => expect(calls.find((c) => c.method === "POST")?.body).toEqual({ nomineeId: "o1" }));
  });

  it("shows no banner outside recovery", async () => {
    const calls = fakeServer({ ...base });
    renderPage(<RecoveryBanner />, { session: hr });
    await waitFor(() => expect(calls.some((c) => c.path === "/admin/health")).toBe(true));
    expect(screen.queryByText(/kurtarma modu/)).toBeNull();
  });
});
