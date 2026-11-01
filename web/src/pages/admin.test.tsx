import { screen, within, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { employee, grant, item, person, step } from "../test/fixtures";
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
const noExceptions = { failedOperations: [], unroutableSteps: [], overlaps: [], suspensionGaps: [], securityRevocations: [], staleGrants: [], reviewFindings: [], reviewFindingsTotal: 0, orphanAccounts: [], totals: { failedOperations: 0, unroutableSteps: 0, overlaps: 0, suspensionGaps: 0, securityRevocations: 0, staleGrants: 0, orphanAccounts: 0 } };
const people = [employee({ id: "o1", fullName: "Can Owner", roles: [] }), employee({ id: "is1", fullName: "Deniz Güvenlik", roles: ["INFOSEC"] }), employee({ id: "sa", fullName: "Okan Admin", roles: ["SUPER_ADMIN"] })];
const base = { "GET /employees": { employees: people }, "GET /admin-groups": { groups: [{ id: "grp1", name: "IT", memberIds: ["o1"] }] }, "GET /approvals": { approvals: [] } };
const jiraAdmin = { id: "t1", name: "Jira", fulfillmentMethod: "API", owner: person("o1", "Can Owner"), adminGroup: { id: "grp1", name: "IT" }, status: "ACTIVE", version: 2,
  levels: [{ id: "l1", name: "Kullanıcı", steps: ["MANAGER"], status: "ACTIVE", version: 1, requestable: true, recommended: false }] };

describe("exceptions", () => {
  it("counts every open record of a capped list and says only the first are listed", async () => {
    fakeServer({ ...base, "GET /exceptions": { ...noExceptions, failedOperations: [op()], totals: { ...noExceptions.totals, failedOperations: 230 } } });
    renderPage(<ExceptionsPage />, { session: sa });
    expect(await screen.findByRole("button", { name: "Tümü (230)" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "İşlem hataları (230)" })).toBeTruthy();
    expect(screen.getByText(/Başarısız işlem: ilk 1 \/ 230/)).toBeTruthy();
  });

  it("retries a failed removal with the operation version", async () => {
    const calls = fakeServer({ ...base, "GET /exceptions": { ...noExceptions, failedOperations: [op()] }, "POST /operations/op1/retry": op({ status: "PENDING" }) });
    renderPage(<ExceptionsPage />, { session: sa });
    expect(await screen.findByText("Araç çağrısı başarısız oldu · 3 deneme")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Yeniden dene" }));
    await waitFor(() => expect(calls.find((c) => c.method === "POST")?.body).toEqual({ version: 4 }));
  });

  it("explains a failed tool call in Turkish and keeps the raw error as a secondary detail (BUG-A-01)", async () => {
    const raw = 'Post "http://mocktools:8081/api/v1/permissions": dial tcp 172.18.0.3:8081: connect: connection refused';
    fakeServer({ ...base, "GET /exceptions": { ...noExceptions, failedOperations: [op({ attempts: 5, lastError: raw }),
      op({ id: "op2", attempts: 0, lastError: "Hesap tedarikçide bulunamadı" })] } });
    renderPage(<ExceptionsPage />, { session: sa });
    expect(await screen.findByText("Araç çağrısı başarısız oldu · 5 deneme")).toBeTruthy();
    expect(screen.getByText(raw).closest("details")).toBeTruthy();
    expect(screen.getByText("Hesap tedarikçide bulunamadı").closest("details")).toBeNull(); // a person's reason is shown as written
  });

  it("offers retry to InfoSec only for tools their admin group runs", async () => {
    const infosec = (groups: unknown[]) => principal({ employee: { id: "is1", email: "is@x.co", fullName: "Deniz Güvenlik", roles: ["INFOSEC"] }, adminGroups: groups }) as never;
    fakeServer({ ...base, "GET /tools": { tools: [jiraAdmin] }, "GET /exceptions": { ...noExceptions, failedOperations: [op()] } });
    const { unmount } = renderPage(<ExceptionsPage />, { session: infosec([]) });
    expect(await screen.findByText("Araç çağrısı başarısız oldu · 3 deneme")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Yeniden dene" })).toBeNull();
    unmount();
    renderPage(<ExceptionsPage />, { session: infosec([{ id: "grp1", name: "IT" }]) });
    expect(await screen.findByRole("button", { name: "Yeniden dene" })).toBeTruthy();
  });

  it("is read-only for an auditor", async () => {
    fakeServer({ ...base, "GET /exceptions": { ...noExceptions, securityRevocations: [op()] } });
    renderPage(<ExceptionsPage />, { session: auditor });
    expect(await screen.findByText("Ayrılan çalışanın kaldırması başarısız")).toBeTruthy(); // op() is FAILED: listed with operation errors
    expect(screen.getByText(/salt okunur/)).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Yeniden dene" })).toBeNull();
  });

  it("lists an unroutable approval step", async () => {
    fakeServer({ ...base, "GET /exceptions": { ...noExceptions, unroutableSteps: [{ step: step({ type: "INFOSEC", assigneeId: null, assigneeName: null }), item: item(), unroutable: true }] } });
    renderPage(<ExceptionsPage />, { session: sa });
    expect(await screen.findByText("Uygun onaylayıcı yok")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Yeniden ata veya geçersiz kıl" })).toBeTruthy();
  });

  it("explains the way out instead of an action when the caller is a party to the request (BUG-A-03)", async () => {
    fakeServer({ ...base, "GET /exceptions": { ...noExceptions, unroutableSteps: [{ step: step({ type: "INFOSEC", assigneeId: null, assigneeName: null }), item: item(), unroutable: true, party: "submitter" }] } });
    renderPage(<ExceptionsPage />, { session: sa });
    expect(await screen.findByText(/talebi siz gönderdiğiniz için/)).toBeTruthy();
    expect(screen.getByText(/talebe taraf olmayan bir Süper Yönetici veya Bilgi Güvenliği üyesi/)).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Yeniden ata veya geçersiz kıl" })).toBeNull();
  });
});

describe("tool administration", () => {
  it("lists tools with their approval chains", async () => {
    fakeServer({ ...base, "GET /tools": { tools: [jiraAdmin] } });
    renderPage(<ToolsAdminPage />, { session: sa });
    expect(await screen.findByText("Jira")).toBeTruthy();
    expect(screen.getByText("Can Owner")).toBeTruthy();
  });

  it("shows each level's approvals as numbered stages in their order", async () => {
    const levels = [{ ...jiraAdmin.levels[0], id: "l1", name: "Admin", steps: ["MANAGER", "TOOL_OWNER", "INFOSEC"] },
      { ...jiraAdmin.levels[0], id: "l2", name: "ReadOnly", steps: ["MANAGER", "TOOL_OWNER"] }];
    fakeServer({ ...base, "GET /tools": { tools: [{ ...jiraAdmin, levels }] } });
    renderPage(<ToolsAdminPage />, { session: sa });
    const admin = (await screen.findByText("Admin")).closest(".level-approvals") as HTMLElement;
    expect(within(admin).getByText("3 aşamalı onay")).toBeTruthy();
    expect(within(admin).getAllByRole("listitem").map((li) => li.textContent)).toEqual(["1Yönetici", "2Araç sahibi", "3Bilgi güvenliği"]);
    const readOnly = screen.getByText("ReadOnly").closest(".level-approvals") as HTMLElement;
    expect(within(readOnly).getByText("2 aşamalı onay")).toBeTruthy();
    expect(within(readOnly).getByRole("list").getAttribute("title")).toMatch(/sırayla/);
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

  it("creates an API tool whose accounts are GitHub usernames", async () => {
    const calls = fakeServer({ ...base, "GET /tools": { tools: [] },
      "POST /tools": () => ({ status: 201, body: { id: "t9", name: "GitHub sandbox", fulfillmentMethod: "API", accountKind: "GITHUB_LOGIN", ownerId: "o1", adminGroupId: "grp1", status: "ACTIVE", version: 1 } }),
      "POST /tools/t9/levels": () => ({ status: 201, body: {} }) });
    renderPage(<ToolEditPage />, { path: "/tools-admin/yeni", pattern: "/tools-admin/:id", session: sa });
    await userEvent.type(await screen.findByLabelText(/Araç adı/), "GitHub sandbox");
    await userEvent.click(screen.getByRole("radio", { name: /Tedarikçi/ }));
    expect(screen.queryByRole("radiogroup", { name: "Hesap kimliği" })).toBeNull(); // only API tools act on another account
    await userEvent.click(screen.getByRole("radio", { name: /API/ }));
    await userEvent.click(within(screen.getByRole("radiogroup", { name: "Hesap kimliği" })).getByRole("radio", { name: /GitHub kullanıcı adı/ }));
    await screen.findByRole("option", { name: /Can Owner/ });
    await userEvent.selectOptions(screen.getByRole("combobox", { name: /Araç sahibi/ }), "o1");
    await userEvent.selectOptions(screen.getByLabelText(/Yönetim grubu/), "grp1");
    await userEvent.type(screen.getByLabelText(/Seviye adı/), "Read");
    await userEvent.click(screen.getByRole("button", { name: "Aracı oluştur" }));
    await waitFor(() => expect(currentPath).toBe("/tools-admin"));
    expect(calls.find((c) => c.method === "POST" && c.path === "/tools")?.body).toEqual({ name: "GitHub sandbox", fulfillmentMethod: "API", accountKind: "GITHUB_LOGIN", ownerId: "o1", adminGroupId: "grp1" });
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
  it("offers direct removal only for grants not already being removed", async () => {
    fakeServer({ ...base, "GET /tools": { tools: [] }, "GET /employees/o1/grants": { grants: [
      grant({ id: "g1", tool: "Jira" }), grant({ id: "g2", tool: "Figma", status: "REVOCATION_FAILED" }), grant({ id: "g3", tool: "Slack", status: "REVOCATION_REQUESTED" })] } });
    renderPage(<DirectGrantPage />, { session: sa });
    await userEvent.click(await screen.findByRole("tab", { name: "Erişimi kaldır" }));
    await screen.findByRole("option", { name: /Can Owner/ });
    await userEvent.selectOptions(screen.getByLabelText(/Çalışan/), "o1");
    expect(await screen.findByText("Jira")).toBeTruthy();
    expect(screen.getAllByRole("button", { name: "Kaldır" })).toHaveLength(1);
  });

  it("renders and grants without crypto.randomUUID, as over plain HTTP on a hostname or IP", async () => {
    const real = globalThis.crypto;
    vi.stubGlobal("crypto", { getRandomValues: (a: Uint8Array<ArrayBuffer>) => real.getRandomValues(a) });
    const calls = fakeServer({ ...base, "GET /tools": { tools: [jiraAdmin] }, "POST /direct-grants": () => ({ status: 201, body: { id: "r5", items: [] } }) });
    renderPage(<DirectGrantPage />, { session: sa });
    await screen.findByRole("option", { name: /Can Owner/ });
    await userEvent.selectOptions(screen.getByLabelText(/Çalışan/), "o1");
    await userEvent.selectOptions(screen.getByLabelText(/^Araç/), "t1");
    await userEvent.selectOptions(screen.getByLabelText(/^Seviye/), "l1");
    await userEvent.type(screen.getByLabelText(/Gerekçe/), "Üretim olayı INC-2301 için acil erişim");
    await userEvent.click(screen.getByRole("button", { name: "Doğrudan erişim ver" }));
    await waitFor(() => expect(currentPath).toBe("/requests/r5"));
    const { submissionId } = calls.find((c) => c.method === "POST")!.body as { submissionId: string };
    expect(submissionId).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
  });

  it("asks for the GitHub username of a GitHub tool and sends it", async () => {
    const github = { ...jiraAdmin, id: "t7", name: "GitHub sandbox", accountKind: "GITHUB_LOGIN", levels: [{ ...jiraAdmin.levels[0], id: "l7", name: "Read" }] };
    const calls = fakeServer({ ...base, "GET /tools": { tools: [github] }, "POST /direct-grants": () => ({ status: 201, body: { id: "r6", items: [] } }) });
    renderPage(<DirectGrantPage />, { session: sa });
    await screen.findByRole("option", { name: /Can Owner/ });
    await userEvent.selectOptions(screen.getByLabelText(/Çalışan/), "o1");
    await userEvent.selectOptions(screen.getByLabelText(/^Araç/), "t7");
    await userEvent.selectOptions(screen.getByLabelText(/^Seviye/), "l7");
    await userEvent.type(screen.getByLabelText(/Gerekçe/), "Üretim olayı INC-2301 için acil erişim");
    await userEvent.click(screen.getByRole("button", { name: "Doğrudan erişim ver" }));
    expect(screen.getByText("GitHub kullanıcı adını girin.")).toBeTruthy();
    await userEvent.type(screen.getByLabelText(/GitHub kullanıcı adı/), " octocat ");
    await userEvent.click(screen.getByRole("button", { name: "Doğrudan erişim ver" }));
    await waitFor(() => expect(currentPath).toBe("/requests/r6"));
    expect(calls.find((c) => c.method === "POST")!.body).toMatchObject({ accessLevelId: "l7", account: "octocat" });
  });

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
    expect(d.getByText(/yalnızca denetim kayıtlarını/)).toBeTruthy();
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

describe("real GitHub API integration", () => {
  const github = { ...jiraAdmin, id: "t7", name: "GitHub demo-project", accountKind: "GITHUB_LOGIN", levels: [{ ...jiraAdmin.levels[0], id: "l7", name: "Read" }] };
  const manual = { ...jiraAdmin, id: "t8", name: "GitHub", fulfillmentMethod: "MANUAL" };
  const page = (id: string) => renderPage(<ToolEditPage />, { path: `/tools-admin/${id}`, pattern: "/tools-admin/:id", session: sa });

  it("shows the real GitHub API only for the tool the backend names, and only reads", async () => {
    const calls = fakeServer({ ...base, "GET /tools": { tools: [jiraAdmin, github, manual] }, "GET /integrations/github": { enabled: true, toolId: "t7" } });
    const view = page("t7");
    expect(await screen.findByText("Gerçek GitHub API entegrasyonu aktif")).toBeTruthy();
    expect(screen.getByText("Erişim davetleri, yetki doğrulama ve erişim kaldırma işlemleri doğrudan GitHub API üzerinden gerçekleştirilir.")).toBeTruthy();
    view.unmount();
    const other = page("t1"); // another API tool stays on the mock
    expect(await screen.findByText("Mock Tool Service kullanılıyor")).toBeTruthy();
    expect(screen.queryByText("Gerçek GitHub API entegrasyonu aktif")).toBeNull();
    other.unmount();
    page("t8"); // a Manual tool has no API service to name
    await screen.findByDisplayValue("GitHub");
    expect(screen.queryByText("Mock Tool Service kullanılıyor")).toBeNull();
    expect(screen.queryByText("Gerçek GitHub API entegrasyonu aktif")).toBeNull();
    expect(calls.every((c) => c.method === "GET")).toBe(true);
    expect(screen.queryByRole("button", { name: /test et/i })).toBeNull();
  });

  it("claims no service when the status cannot be read", async () => {
    fakeServer({ ...base, "GET /tools": { tools: [github] }, "GET /integrations/github": () => ({ status: 500, body: { error: "internal_error" } }) });
    page("t7");
    await screen.findByDisplayValue("GitHub demo-project");
    await waitFor(() => expect(screen.queryByText("Mock Tool Service kullanılıyor")).toBeNull());
    expect(screen.queryByText("Gerçek GitHub API entegrasyonu aktif")).toBeNull();
  });

  it("shows the mock for the GitHub tool when the adapter is off", async () => {
    fakeServer({ ...base, "GET /tools": { tools: [github] }, "GET /integrations/github": { enabled: false, toolId: null } });
    page("t7");
    expect(await screen.findByText("Mock Tool Service kullanılıyor")).toBeTruthy();
    expect(screen.queryByText("Gerçek GitHub API entegrasyonu aktif")).toBeNull();
  });

  it("points a reviewer of the default setup to the optional adapter", async () => {
    fakeServer({ ...base, "GET /tools": { tools: [jiraAdmin] }, "GET /integrations/github": { enabled: false, toolId: null } });
    renderPage(<ToolsAdminPage />, { session: sa });
    expect(await screen.findByText(/İsteğe bağlı gerçek GitHub API/)).toBeTruthy();
    expect(await screen.findByText(/Bu örnekte etkin değil/)).toBeTruthy(); // shown once the backend answered
  });

  it("explains a GitHub provisioning failure in Turkish, keeping the technical detail", async () => {
    fakeServer({ ...base, "GET /exceptions": { ...noExceptions, failedOperations: [
      op({ id: "op2", type: "GRANT", tool: "GitHub demo-project", lastError: "the GitHub account has write access that AccessFlow did not grant; review it by hand" }),
      op({ id: "op3", type: "GRANT", tool: "GitHub demo-project", lastError: "github status 422" })] } });
    renderPage(<ExceptionsPage />, { session: sa });
    expect(await screen.findByText(/GitHub hesabında AccessFlow’un vermediği bir erişim var/)).toBeTruthy();
    expect(screen.getByText(/GitHub isteği reddetti \(HTTP 422\)/)).toBeTruthy();
    expect(screen.getAllByText("Teknik ayrıntı")).toHaveLength(2);
  });
});
