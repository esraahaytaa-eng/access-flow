import { screen, within, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { employee, item, person, step } from "../test/fixtures";
import { currentPath, renderPage } from "../test/render";
import { fakeServer, principal } from "../test/server";
import { addDays, todayIn } from "../ui/format";
import { DirectGrantPage } from "./DirectGrant";
import { ExceptionsPage } from "./Exceptions";
import { SettingsPage } from "./Settings";
import { ToolEditPage } from "./ToolEdit";
import { ToolsAdminPage } from "./ToolsAdmin";

afterEach(() => vi.unstubAllGlobals());

const sa = principal({ employee: { id: "sa", email: "sa@x.co", fullName: "Okan Admin", roles: ["SUPER_ADMIN"] } }) as never;
const auditor = principal({ employee: { id: "au", email: "au@x.co", fullName: "Gizem Denetçi", roles: ["AUDITOR"] } }) as never;
const dialog = (name: string) => within(screen.getByRole("dialog", { name }));
const op = (over: Record<string, unknown> = {}) => ({ id: "op1", grantId: "g1", type: "REVOKE", employee: person("e2", "Deniz Kara"), tool: "Jira", level: "Kullanıcı", status: "FAILED", attempts: 3, lastError: "timeout", failedAt: "2026-10-28T09:00:00Z", version: 4, createdAt: "2026-10-28T08:00:00Z", ...over });
const noExceptions = { failedOperations: [], unroutableSteps: [], overlaps: [], suspensionGaps: [], securityRevocations: [], staleGrants: [] };
const people = [employee({ id: "o1", fullName: "Can Owner", roles: [] }), employee({ id: "is1", fullName: "Deniz Güvenlik", roles: ["INFOSEC"] }), employee({ id: "sa", fullName: "Okan Admin", roles: ["SUPER_ADMIN"] })];
const base = { "GET /employees": { employees: people }, "GET /admin-groups": { groups: [{ id: "grp1", name: "IT", memberIds: ["o1"] }] }, "GET /approvals": { approvals: [] } };
const jiraAdmin = { id: "t1", name: "Jira", fulfillmentMethod: "API", owner: person("o1", "Can Owner"), adminGroup: { id: "grp1", name: "IT" }, status: "ACTIVE", version: 2,
  levels: [{ id: "l1", name: "Kullanıcı", steps: ["MANAGER"], status: "ACTIVE", version: 1, requestable: true, recommended: false }] };

describe("exceptions", () => {
  it("retries a failed removal with the operation version", async () => {
    const calls = fakeServer({ ...base, "GET /exceptions": { ...noExceptions, failedOperations: [op()] }, "POST /operations/op1/retry": op({ status: "PENDING" }) });
    renderPage(<ExceptionsPage />, { session: sa });
    expect(await screen.findByText("timeout · 3 deneme")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Yeniden dene" }));
    await waitFor(() => expect(calls.find((c) => c.method === "POST")?.body).toEqual({ version: 4 }));
  });

  it("offers retry to InfoSec only for tools their admin group runs", async () => {
    const infosec = (groups: unknown[]) => principal({ employee: { id: "is1", email: "is@x.co", fullName: "Deniz Güvenlik", roles: ["INFOSEC"] }, adminGroups: groups }) as never;
    fakeServer({ ...base, "GET /tools": { tools: [jiraAdmin] }, "GET /exceptions": { ...noExceptions, failedOperations: [op()] } });
    const { unmount } = renderPage(<ExceptionsPage />, { session: infosec([]) });
    expect(await screen.findByText("timeout · 3 deneme")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Yeniden dene" })).toBeNull();
    unmount();
    renderPage(<ExceptionsPage />, { session: infosec([{ id: "grp1", name: "IT" }]) });
    expect(await screen.findByRole("button", { name: "Yeniden dene" })).toBeTruthy();
  });

  it("is read-only for an auditor", async () => {
    fakeServer({ ...base, "GET /exceptions": { ...noExceptions, securityRevocations: [op()] } });
    renderPage(<ExceptionsPage />, { session: auditor });
    expect(await screen.findByText("Ayrılan çalışanın erişimi sürüyor")).toBeTruthy();
    expect(screen.getByText(/salt okunur/)).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Yeniden dene" })).toBeNull();
  });

  it("lists an unroutable approval step", async () => {
    fakeServer({ ...base, "GET /exceptions": { ...noExceptions, unroutableSteps: [{ step: step({ type: "INFOSEC", assigneeId: null, assigneeName: null }), item: item(), unroutable: true }] } });
    renderPage(<ExceptionsPage />, { session: sa });
    expect(await screen.findByText("Uygun onaylayıcı yok")).toBeTruthy();
  });
});

describe("tool administration", () => {
  it("lists tools with their approval chains", async () => {
    fakeServer({ ...base, "GET /tools": { tools: [jiraAdmin] } });
    renderPage(<ToolsAdminPage />, { session: sa });
    expect(await screen.findByText("Jira")).toBeTruthy();
    expect(screen.getByText("Can Owner")).toBeTruthy();
  });

  it("creates a tool and then its levels", async () => {
    const calls = fakeServer({ ...base, "GET /tools": { tools: [] },
      "POST /tools": () => ({ status: 201, body: { id: "t9", name: "Figma", fulfillmentMethod: "VENDOR", ownerId: "o1", adminGroupId: "grp1", status: "ACTIVE", version: 1 } }),
      "POST /tools/t9/levels": () => ({ status: 201, body: {} }) });
    renderPage(<ToolEditPage />, { path: "/tools-admin/yeni", pattern: "/tools-admin/:id", session: sa });
    await userEvent.type(await screen.findByLabelText(/Araç adı/), "Figma");
    await userEvent.click(screen.getByRole("radio", { name: /Tedarikçi/ }));
    await screen.findByRole("option", { name: /Can Owner/ });
    await userEvent.selectOptions(screen.getByRole("combobox", { name: /Araç sahibi/ }), "o1");
    await userEvent.selectOptions(screen.getByLabelText(/Yönetim grubu/), "grp1");
    await userEvent.type(screen.getByLabelText(/Seviye adı/), "Editör");
    await userEvent.click(screen.getByRole("switch", { name: "Bilgi güvenliği onayı" }));
    await userEvent.click(screen.getByRole("button", { name: "Aracı oluştur" }));
    await waitFor(() => expect(currentPath).toBe("/tools-admin"));
    expect(calls.filter((c) => c.method === "POST").map((c) => [c.path, c.body])).toEqual([
      ["/tools", { name: "Figma", fulfillmentMethod: "VENDOR", ownerId: "o1", adminGroupId: "grp1" }],
      ["/tools/t9/levels", { name: "Editör", requiresToolOwner: false, requiresInfosec: true }],
    ]);
  });

  it("confirms a policy change before saving a level's steps", async () => {
    const calls = fakeServer({ ...base, "GET /tools": { tools: [jiraAdmin] }, "PUT /tools/t1/levels/l1": { id: "l1" } });
    renderPage(<ToolEditPage />, { path: "/tools-admin/t1", pattern: "/tools-admin/:id", session: sa });
    await userEvent.click(await screen.findByRole("switch", { name: "Araç sahibi onayı" }));
    await userEvent.click(screen.getByRole("button", { name: "Değişiklikleri kaydet" }));
    await userEvent.click(dialog("Politika değişikliğini onaylayın").getByRole("button", { name: "Kaydet ve uygula" }));
    await waitFor(() => expect(calls.find((c) => c.method === "PUT")?.body).toEqual({ name: "Kullanıcı", requiresToolOwner: true, requiresInfosec: false, version: 1 }));
    expect(calls.some((c) => c.path === "/tools/t1")).toBe(false);
  });
});

describe("tool administration after a failed save", () => {
  it("opens the created tool when one of its levels fails", async () => {
    fakeServer({ ...base, "GET /tools": { tools: [] },
      "POST /tools": () => ({ status: 201, body: { id: "t9", name: "Figma", fulfillmentMethod: "API", ownerId: "o1", adminGroupId: "grp1", status: "ACTIVE", version: 1 } }),
      "POST /tools/t9/levels": () => ({ status: 409, body: { error: "level_name_taken", field: "name" } }) });
    renderPage(<ToolEditPage />, { path: "/tools-admin/yeni", pattern: "/tools-admin/:id", session: sa });
    await userEvent.type(await screen.findByLabelText(/Araç adı/), "Figma");
    await userEvent.click(screen.getByRole("radio", { name: /API/ }));
    await screen.findByRole("option", { name: /Can Owner/ });
    await userEvent.selectOptions(screen.getByRole("combobox", { name: /Araç sahibi/ }), "o1");
    await userEvent.selectOptions(screen.getByLabelText(/Yönetim grubu/), "grp1");
    await userEvent.type(screen.getByLabelText(/Seviye adı/), "Editör");
    await userEvent.click(screen.getByRole("button", { name: "Aracı oluştur" }));
    await waitFor(() => expect(currentPath).toBe("/tools-admin/t9"));
  });

  it("reloads the saved levels when a level save fails", async () => {
    let level = jiraAdmin.levels[0];
    fakeServer({ ...base, "GET /tools": () => ({ body: { tools: [{ ...jiraAdmin, levels: [level] }] } }),
      "PUT /tools/t1/levels/l1": () => { level = { ...level, version: 2 }; return { status: 409, body: { error: "version_conflict" } }; } });
    renderPage(<ToolEditPage />, { path: "/tools-admin/t1", pattern: "/tools-admin/:id", session: sa });
    const name = await screen.findByLabelText(/Seviye adı/);
    await userEvent.clear(name);
    await userEvent.type(name, "Okuyucu");
    await userEvent.click(screen.getByRole("button", { name: "Değişiklikleri kaydet" }));
    await waitFor(() => expect((screen.getByLabelText(/Seviye adı/) as HTMLInputElement).value).toBe("Kullanıcı"));
  });
});

describe("direct grant", () => {
  it("grants for at most 90 days counting today and opens the request", async () => {
    const calls = fakeServer({ ...base, "GET /tools": { tools: [jiraAdmin] }, "POST /direct-grants": () => ({ status: 201, body: { id: "r5", items: [] } }) });
    renderPage(<DirectGrantPage />, { session: sa });
    await screen.findByRole("option", { name: /Can Owner/ });
    await userEvent.selectOptions(screen.getByLabelText(/Çalışan/), "o1");
    await userEvent.selectOptions(screen.getByLabelText(/^Araç/), "t1");
    await userEvent.selectOptions(screen.getByLabelText(/^Seviye/), "l1");
    await userEvent.clear(screen.getByLabelText("Gün sayısı"));
    await userEvent.type(screen.getByLabelText("Gün sayısı"), "120");
    await userEvent.type(screen.getByLabelText(/Gerekçe/), "Üretim olayı INC-2301 için acil erişim");
    await userEvent.click(screen.getByRole("button", { name: "Doğrudan erişim ver" }));
    expect(screen.getByText("2 ile 90 gün arasında bir süre girin.")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "90" }));
    await userEvent.click(screen.getByRole("button", { name: "Doğrudan erişim ver" }));
    await waitFor(() => expect(currentPath).toBe("/requests/r5"));
    const body = calls.find((c) => c.method === "POST")!.body as Record<string, unknown>;
    expect(body).toEqual({ submissionId: body.submissionId, beneficiaryId: "o1", accessLevelId: "l1", justification: "Üretim olayı INC-2301 için acil erişim", expiresOn: addDays(todayIn("Europe/Istanbul"), 89) });
  });
});

describe("settings", () => {
  it("assigns a role", async () => {
    const calls = fakeServer({ ...base, "GET /tools": { tools: [] }, "POST /employees/o1/roles": () => ({ status: 204 }) });
    renderPage(<SettingsPage />, { session: sa });
    await userEvent.click(await screen.findByRole("button", { name: "Rol ata" }));
    const d = dialog("Rol ata");
    await userEvent.selectOptions(d.getByLabelText(/Kişi/), "o1");
    await userEvent.selectOptions(d.getByLabelText(/^Rol/), "AUDITOR");
    expect(d.getByText(/salt okunurdur/)).toBeTruthy();
    await userEvent.click(d.getByRole("button", { name: "Ata" }));
    await waitFor(() => expect(calls.find((c) => c.method === "POST")?.body).toEqual({ role: "AUDITOR" }));
  });

  it("creates a group and adds a member", async () => {
    const calls = fakeServer({ ...base, "GET /tools": { tools: [] }, "POST /admin-groups": () => ({ status: 201, body: { id: "grp2", name: "Finans BT" } }), "POST /admin-groups/grp1/members": () => ({ status: 204 }) });
    renderPage(<SettingsPage />, { session: sa });
    await userEvent.click(await screen.findByRole("tab", { name: "Yönetim grupları" }));
    await userEvent.type(screen.getByLabelText("Grup adı"), "Finans BT");
    await userEvent.click(screen.getByRole("button", { name: "Oluştur" }));
    await userEvent.selectOptions(screen.getByLabelText("Üye ekle"), "is1");
    await userEvent.click(screen.getByRole("button", { name: "Ekle" }));
    await waitFor(() => expect(calls.filter((c) => c.method === "POST").map((c) => [c.path, c.body])).toEqual([
      ["/admin-groups", { name: "Finans BT" }], ["/admin-groups/grp1/members", { employeeId: "is1" }],
    ]));
  });
});
