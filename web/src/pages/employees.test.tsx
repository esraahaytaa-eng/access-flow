import { screen, within, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { employee, grant, person } from "../test/fixtures";
import { renderPage } from "../test/render";
import { fakeServer, principal } from "../test/server";
import { EmployeeDetailPage } from "./EmployeeDetail";
import { EmployeesPage } from "./Employees";

afterEach(() => vi.unstubAllGlobals());

const hr = principal({ employee: { id: "hr", email: "hr@x.co", fullName: "Ayşe İK", roles: ["HR_MANAGER"] } }) as never;
const manager = employee({ id: "m1", fullName: "Burak Manager", managerId: null, department: "Ürün", jobTitle: "Lider" });
const manager2 = employee({ id: "m2", fullName: "Selin Lead", managerId: null, department: "Platform", jobTitle: "Lider" });
const entry = (name: string, active = true) => ({ id: name, name, active });
const catalog = { departments: [entry("Ürün"), entry("Platform"), entry("Eski Birim", false)], jobPositions: [entry("Mühendis"), entry("Analist"), entry("Kıdemli Analist"), entry("Lider")] };
const dir = (...extra: unknown[]) => ({ "GET /employees": { employees: [manager, manager2, employee(), ...extra] }, "GET /tools": { tools: [] }, "GET /approvals": { approvals: [] }, "GET /starter-packages": { entries: [] }, "GET /company-catalog": catalog });
const detail = (routes: Record<string, unknown>) => renderPage(<EmployeeDetailPage />, { path: "/employees/e2", pattern: "/employees/:id", session: hr }) && routes;
const dialog = (name: string) => within(screen.getByRole("dialog", { name }));

describe("employees", () => {
  it("creates an employee, whose activation link goes only to the Mock Email Outbox", async () => {
    const calls = fakeServer({ ...dir(), "POST /employees": () => ({ status: 201, body: { employee: employee({ id: "n1", fullName: "Kaan Uslu" }) } }) });
    renderPage(<EmployeesPage />, { session: hr });
    await userEvent.click(await screen.findByRole("button", { name: "Yeni çalışan" }));
    const d = dialog("Yeni çalışan");
    await userEvent.type(d.getByLabelText(/Ad soyad/), "Kaan Uslu");
    await userEvent.type(d.getByLabelText(/E-posta/), "kaan@x.co");
    expect(within(d.getByLabelText(/Departman/)).queryByText("Eski Birim")).toBeNull(); // retired entries are not offered
    await userEvent.selectOptions(d.getByLabelText(/Unvan/), "Mühendis");
    await userEvent.selectOptions(d.getByLabelText(/Departman/), "Ürün");
    await userEvent.selectOptions(d.getByLabelText(/Yönetici/), "m1");
    await userEvent.click(d.getByRole("button", { name: "Çalışanı oluştur" }));
    expect(await screen.findByText(/Etkinleştirme bağlantısı Mock Email Outbox/)).toBeTruthy();
    expect(screen.queryByLabelText("Geçici parola")).toBeNull();
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({ email: "kaan@x.co", fullName: "Kaan Uslu", department: "Ürün", jobTitle: "Mühendis", managerId: "m1" });
  });

  it("exports the employees shown as CSV, with manager names and Turkish headers", async () => {
    let blob: Blob | undefined;
    URL.createObjectURL = vi.fn((b: Blob) => { blob = b; return "blob:x"; });
    URL.revokeObjectURL = vi.fn();
    fakeServer(dir(employee({ id: "t1", fullName: "Ayrılan Kişi", status: "TERMINATED", managerId: "m1" })));
    renderPage(<EmployeesPage />, { session: hr });
    await userEvent.click(await screen.findByRole("button", { name: "CSV indir" }));
    const rows = (await blob!.text()).replace(/^\uFEFF/, "").split("\r\n");
    expect(rows[0]).toBe('"Ad soyad","E-posta","Departman","Unvan","Yönetici","Durum","Roller"');
    expect(rows).toHaveLength(4); // the active employees shown, not the terminated one
    expect(rows).toContain(`"${employee().fullName}","${employee().email}","${employee().department}","${employee().jobTitle}","Burak Manager","Aktif",""`);
  });

  it("manages departments and job positions in the company catalog", async () => {
    const calls = fakeServer({ ...dir(), "POST /company-catalog/departments": () => ({ status: 201, body: entry("Finans") }),
      "POST /company-catalog/job-positions/Lider/retire": entry("Lider", false) });
    renderPage(<EmployeesPage />, { session: hr });
    await userEvent.click(await screen.findByRole("button", { name: "Departmanlar ve unvanlar" }));
    const d = dialog("Departmanlar ve unvanlar");
    expect(await d.findByText("Eski Birim (kullanımdan kaldırıldı)")).toBeTruthy();
    expect(d.queryByRole("button", { name: "Eski Birim kaldır" })).toBeNull();
    await userEvent.type(d.getByLabelText("Yeni departmanlar adı"), "Finans");
    await userEvent.click(d.getAllByRole("button", { name: "Ekle" })[0]);
    await userEvent.click(d.getByRole("button", { name: "Lider kaldır" }));
    await waitFor(() => expect(calls.filter((c) => c.method === "POST").map((c) => [c.path, c.body])).toEqual([
      ["/company-catalog/departments", { name: "Finans" }], ["/company-catalog/job-positions/Lider/retire", undefined]]));
  });

  it("filters the directory by status", async () => {
    fakeServer(dir(employee({ id: "e3", fullName: "Eski Çalışan", status: "TERMINATED" })));
    renderPage(<EmployeesPage />, { session: hr });
    expect(await screen.findByText("Deniz Kara")).toBeTruthy();
    expect(screen.queryByText("Eski Çalışan")).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: /Ayrılan/ }));
    expect(screen.getByText("Eski Çalışan")).toBeTruthy();
  });
});

describe("employee detail", () => {
  it("suspends with the employee version and a reason", async () => {
    const calls = fakeServer({ ...dir(), "GET /employees/e2/grants": { grants: [] }, "POST /employees/e2/suspend": employee({ status: "SUSPENDED" }) });
    detail({});
    await userEvent.click(await screen.findByRole("button", { name: "Askıya al" }));
    await userEvent.type(dialog("Deniz Kara askıya alınsın mı?").getByLabelText(/Gerekçe/), "Uzun süreli izin");
    await userEvent.click(dialog("Deniz Kara askıya alınsın mı?").getByRole("button", { name: "Askıya al" }));
    await waitFor(() => expect(calls.find((c) => c.method === "POST")?.body).toEqual({ version: 5, reason: "Uzun süreli izin" }));
  });

  it("schedules a termination at 18:00 company time", async () => {
    const calls = fakeServer({ ...dir(), "GET /employees/e2/grants": { grants: [] }, "PUT /employees/e2/termination": employee() });
    detail({});
    await userEvent.click(await screen.findByRole("button", { name: "İşten çıkış" }));
    const d = dialog("Deniz Kara · işten çıkış");
    await userEvent.click(d.getByRole("button", { name: "Planlı" }));
    await userEvent.type(d.getByLabelText(/Geçerlilik tarihi/), "2099-11-15");
    await userEvent.type(d.getByLabelText(/Ayrılış nedeni/), "İstifa");
    await userEvent.click(d.getByRole("button", { name: "Çıkışı planla" }));
    await waitFor(() => expect(calls.find((c) => c.method === "PUT")?.body).toEqual({ effectiveAt: "2099-11-15T15:00:00.000Z", reason: "İstifa", version: 5 }));
  });

  it("offers immediate termination while one is scheduled, and explains a scheduled one still waiting (BUG-B-04)", async () => {
    const target = employee({ roles: ["SUPER_ADMIN"], termination: { at: "2026-10-01T15:00:00Z", reason: "İstifa", scheduledBy: "hr" } });
    fakeServer({ ...dir(), "GET /employees": { employees: [manager, manager2, target] }, "GET /employees/e2/grants": { grants: [] }, "GET /super-admin-nominations": { nominations: [] } });
    detail({});
    expect(await screen.findByRole("button", { name: "Planlı çıkışı iptal et" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "İşten çıkış" })).toBeTruthy();
    expect(screen.getByText(/Planlanan zaman geçti/).textContent).toContain("yeni Süper Yönetici rolü kabul edene kadar");
  });

  it("blocks the last Super Admin's termination until a successor accepts, and lets HR appoint one (D2 succession)", async () => {
    const target = employee({ roles: ["SUPER_ADMIN"] });
    const calls = fakeServer({ ...dir(), "GET /employees": { employees: [manager, manager2, target] }, "GET /employees/e2/grants": { grants: [] },
      "GET /super-admin-nominations": { nominations: [] },
      "POST /super-admin-nominations": () => ({ status: 201, body: { id: "n1", nominee: { id: "m1", fullName: "Burak Manager" }, proposedBy: { id: "hr", fullName: "Ayşe İK" },
        recovery: false, succession: true, status: "PENDING", createdAt: "2026-10-30T09:00:00Z", expiresAt: "2026-10-31T09:00:00Z", version: 1, canAuthorize: false } }) });
    detail({});
    await userEvent.click(await screen.findByRole("button", { name: "İşten çıkış" }));
    const d = dialog("Deniz Kara · işten çıkış");
    expect(d.getByText(/Bu çalışan sistemin son Süper Yöneticisidir. İşten çıkış işlemini tamamlamak için önce yeni bir Süper Yönetici atanmalıdır./)).toBeTruthy();
    expect((d.getByRole("button", { name: "Hemen işten çıkar" }) as HTMLButtonElement).disabled).toBe(true);
    await userEvent.click(d.getByRole("button", { name: "Yeni Süper Yönetici ata" }));
    const pick = dialog("Yeni Süper Yönetici ata");
    expect(within(pick.getByLabelText(/Aday/)).queryByText(/Ayşe İK/)).toBeNull(); // never oneself
    await userEvent.selectOptions(pick.getByLabelText(/Aday/), "m1");
    await userEvent.click(pick.getByRole("button", { name: "Yetkilendirmeye gönder" }));
    await waitFor(() => expect(calls.find((c) => c.method === "POST")?.body).toEqual({ nomineeId: "m1" }));
    expect(calls.some((c) => c.path.endsWith("/terminate"))).toBe(false);
  });

  it("shows HR the authorized successor's status and reissues an expired link", async () => {
    const target = employee({ roles: ["SUPER_ADMIN"], status: "SUSPENDED" });
    const n = { id: "n1", nominee: { id: "m1", fullName: "Burak Manager" }, proposedBy: { id: "hr", fullName: "Ayşe İK" }, recovery: true, succession: true,
      status: "EXPIRED", createdAt: "2026-10-28T09:00:00Z", expiresAt: "2026-10-29T09:00:00Z", version: 2, canAuthorize: false, authorizedAt: "2026-10-28T10:00:00Z" };
    const calls = fakeServer({ ...dir(), "GET /employees": { employees: [manager, manager2, target] }, "GET /employees/e2/grants": { grants: [] },
      "GET /super-admin-nominations": { nominations: [n] }, "POST /super-admin-nominations/n1/reissue": { ...n, status: "PENDING", version: 3 } });
    detail({});
    await userEvent.click(await screen.findByRole("button", { name: "İşten çıkış" }));
    const d = dialog("Deniz Kara · işten çıkış");
    expect(await d.findByText(/Burak Manager · bağlantının süresi doldu/)).toBeTruthy();
    expect(d.queryByRole("button", { name: "Yeni Süper Yönetici ata" })).toBeNull();
    await userEvent.click(d.getByRole("button", { name: "Bağlantıyı yeniden gönder" }));
    await waitFor(() => expect(calls.find((c) => c.method === "POST")?.body).toEqual({ version: 2 }));
  });

  it("does not warn while another Super Admin stays", async () => {
    fakeServer({ ...dir(), "GET /employees": { employees: [manager, employee({ id: "sa", roles: ["SUPER_ADMIN"] }), employee({ roles: ["SUPER_ADMIN"] })] }, "GET /employees/e2/grants": { grants: [] } });
    renderPage(<EmployeeDetailPage />, { path: "/employees/e2", pattern: "/employees/:id", session: hr });
    await userEvent.click(await screen.findByRole("button", { name: "İşten çıkış" }));
    expect(dialog("Deniz Kara · işten çıkış").queryByText(/son Süper Yöneticisidir/)).toBeNull();
  });

  it("transfers to a new manager, keeping one grant with a justification", async () => {
    const grants = [grant({ id: "g1", employee: person("e2", "Deniz Kara") }), grant({ id: "g2", tool: "Figma", employee: person("e2", "Deniz Kara") })];
    const calls = fakeServer({ ...dir(), "GET /employees/e2/grants": { grants }, "POST /employees/e2/transfer": employee({ managerId: "m2" }) });
    detail({});
    await userEvent.click(await screen.findByRole("button", { name: "Transfer" }));
    const d = dialog("Deniz Kara · ekip değişikliği");
    await userEvent.selectOptions(d.getByLabelText("Yeni yönetici"), "m2");
    const figma = d.getByText("Figma").closest(".card") as HTMLElement;
    await userEvent.click(within(figma).getByRole("button", { name: "Koru" }));
    await userEvent.type(within(figma).getByLabelText(/Koruma gerekçesi/), "Ortak proje Aralık sonuna kadar");
    await userEvent.type(d.getByLabelText(/Transfer gerekçesi/), "Ekip değişikliği");
    await userEvent.click(d.getByRole("button", { name: "Transferi onayla" }));
    await waitFor(() => expect(calls.find((c) => c.method === "POST")?.body).toEqual({
      version: 5, managerId: "m2", department: "Ürün", jobTitle: "Analist", reason: "Ekip değişikliği",
      keep: [{ grantId: "g2", justification: "Ortak proje Aralık sonuna kadar" }],
    }));
  });

  it("saves a title-only change without a transfer", async () => {
    const calls = fakeServer({ ...dir(), "GET /employees/e2/grants": { grants: [] }, "PUT /employees/e2": employee({ jobTitle: "Kıdemli Analist" }) });
    detail({});
    await userEvent.click(await screen.findByRole("button", { name: "Transfer" }));
    const d = dialog("Deniz Kara · ekip değişikliği");
    await userEvent.selectOptions(d.getByLabelText(/Unvan/), "Kıdemli Analist");
    await userEvent.click(d.getByRole("button", { name: "Kaydet" }));
    await waitFor(() => expect(calls.find((c) => c.method === "PUT")?.body).toEqual({ version: 5, managerId: "m1", department: "Ürün", jobTitle: "Kıdemli Analist" }));
  });

  it("keeps a retired current value selectable when transferring", async () => {
    fakeServer({ ...dir(), "GET /employees": { employees: [manager, employee({ department: "Eski Birim" })] }, "GET /employees/e2/grants": { grants: [] } });
    renderPage(<EmployeeDetailPage />, { path: "/employees/e2", pattern: "/employees/:id", session: hr });
    await userEvent.click(await screen.findByRole("button", { name: "Transfer" }));
    const select = dialog("Deniz Kara · ekip değişikliği").getByLabelText(/Yeni departman/) as HTMLSelectElement;
    expect(select.value).toBe("Eski Birim");
    expect(within(select).getByText("Eski Birim (kullanımdan kaldırıldı)")).toBeTruthy();
  });

  it("blocks transfer and termination until the access list has loaded", async () => {
    fakeServer({ ...dir(), "GET /employees/e2/grants": () => ({ status: 500, body: { error: "internal_error" } }) });
    detail({});
    expect(await screen.findByText("Erişimler yüklenemedi")).toBeTruthy();
    expect((screen.getByRole("button", { name: "Transfer" }) as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByRole("button", { name: "İşten çıkış" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("transfers someone who has no job title", async () => {
    const untitled = employee({ id: "e3", fullName: "Ece Unvansız", jobTitle: null });
    const calls = fakeServer({ ...dir(untitled), "GET /employees/e3/grants": { grants: [] }, "POST /employees/e3/transfer": untitled });
    renderPage(<EmployeeDetailPage />, { path: "/employees/e3", pattern: "/employees/:id", session: hr });
    await userEvent.click(await screen.findByRole("button", { name: "Transfer" }));
    const d = dialog("Ece Unvansız · ekip değişikliği");
    await userEvent.selectOptions(d.getByLabelText("Yeni yönetici"), "m2");
    await userEvent.type(d.getByLabelText(/Transfer gerekçesi/), "Platform ekibine geçiş");
    await userEvent.click(d.getByRole("button", { name: "Transferi onayla" }));
    await waitFor(() => expect(calls.some((c) => c.path === "/employees/e3/transfer")).toBe(true));
  });

  it("rehires a leaver and shows the server's refusal when they still own tools", async () => {
    let refuse = true;
    fakeServer({
      ...dir(), "GET /employees": { employees: [manager, employee({ status: "TERMINATED", managerId: null })] }, "GET /employees/e2/grants": { grants: [] },
      "POST /employees/e2/rehire": () => refuse ? { status: 409, body: { error: "owns_tools" } } : { body: { employee: employee() } },
    });
    detail({});
    await userEvent.click(await screen.findByRole("button", { name: "Yeniden işe al" }));
    const d = dialog("Deniz Kara · yeniden işe alım");
    await userEvent.selectOptions(d.getByLabelText(/Yönetici/), "m1");
    await userEvent.type(d.getByLabelText(/Gerekçe/), "Geri döndü");
    await userEvent.click(d.getByRole("button", { name: "Yeniden işe al" }));
    expect(await screen.findByText(/araç sahibi/i)).toBeTruthy();
    refuse = false;
    await userEvent.click(d.getByRole("button", { name: "Yeniden işe al" }));
    expect(await screen.findByText(/Etkinleştirme bağlantısı Mock Email Outbox/)).toBeTruthy();
  });

  it("offers no actions on one's own record", async () => {
    fakeServer({ ...dir(employee({ id: "hr", fullName: "Ayşe İK" })), "GET /employees/hr/grants": { grants: [] } });
    renderPage(<EmployeeDetailPage />, { path: "/employees/hr", pattern: "/employees/:id", session: hr });
    expect(await screen.findByText(/Kendi çalışan kaydınızı değiştiremezsiniz/)).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Askıya al" })).toBeNull();
  });
});
