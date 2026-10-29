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
const dir = (...extra: unknown[]) => ({ "GET /employees": { employees: [manager, manager2, employee(), ...extra] }, "GET /tools": { tools: [] }, "GET /approvals": { approvals: [] }, "GET /starter-packages": { entries: [] } });
const detail = (routes: Record<string, unknown>) => renderPage(<EmployeeDetailPage />, { path: "/employees/e2", pattern: "/employees/:id", session: hr }) && routes;
const dialog = (name: string) => within(screen.getByRole("dialog", { name }));

describe("employees", () => {
  it("creates an employee and shows the temporary password once", async () => {
    const calls = fakeServer({ ...dir(), "POST /employees": () => ({ status: 201, body: { employee: employee({ id: "n1", fullName: "Kaan Uslu" }), temporaryPassword: "Tmp-Secret-123456" } }) });
    renderPage(<EmployeesPage />, { session: hr });
    await userEvent.click(await screen.findByRole("button", { name: "Yeni çalışan" }));
    const d = dialog("Yeni çalışan");
    await userEvent.type(d.getByLabelText(/Ad soyad/), "Kaan Uslu");
    await userEvent.type(d.getByLabelText(/E-posta/), "kaan@x.co");
    await userEvent.type(d.getByLabelText(/Unvan/), "Mühendis");
    await userEvent.type(d.getByLabelText(/Departman/), "Ürün");
    await userEvent.selectOptions(d.getByLabelText(/Yönetici/), "m1");
    await userEvent.click(d.getByRole("button", { name: "Çalışanı oluştur" }));
    expect(await screen.findByText("Tmp-Secret-123456")).toBeTruthy();
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({ email: "kaan@x.co", fullName: "Kaan Uslu", department: "Ürün", jobTitle: "Mühendis", managerId: "m1" });
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

  it("warns and asks for confirmation before the last Super Admin leaves (D2 recovery)", async () => {
    const sa = principal({ employee: { id: "sa", email: "sa@x.co", fullName: "Okan Admin", roles: ["SUPER_ADMIN"] } }) as never;
    const scheduledAdmin = employee({ id: "sa", fullName: "Okan Admin", roles: ["SUPER_ADMIN"], termination: { at: "2099-12-01T15:00:00Z", reason: "İstifa", scheduledBy: "x" } });
    const target = employee({ roles: ["SUPER_ADMIN"] });
    const calls = fakeServer({ ...dir(), "GET /employees": { employees: [manager, scheduledAdmin, target] }, "GET /employees/e2/grants": { grants: [] },
      "PUT /employees/e2/termination": employee() });
    renderPage(<EmployeeDetailPage />, { path: "/employees/e2", pattern: "/employees/:id", session: sa });
    await userEvent.click(await screen.findByRole("button", { name: "İşten çıkış" }));
    const d = dialog("Deniz Kara · işten çıkış");
    expect(d.getByText("Sistem Yönetici Kurtarma moduna girecek")).toBeTruthy();
    await userEvent.click(d.getByRole("button", { name: "Planlı" }));
    await userEvent.type(d.getByLabelText(/Geçerlilik tarihi/), "2099-11-15");
    await userEvent.type(d.getByLabelText(/Ayrılış nedeni/), "İstifa");
    await userEvent.click(d.getByRole("button", { name: "Çıkışı planla" }));
    expect(await d.findByText("Devam etmek için onaylayın.")).toBeTruthy();
    expect(calls.some((c) => c.method === "PUT")).toBe(false);
    await userEvent.click(d.getByLabelText(/kurtarma moduna gireceğini anlıyorum/));
    await userEvent.click(d.getByRole("button", { name: "Çıkışı planla" }));
    await waitFor(() => expect(calls.some((c) => c.method === "PUT")).toBe(true));
  });

  it("does not warn while another Super Admin stays", async () => {
    fakeServer({ ...dir(), "GET /employees": { employees: [manager, employee({ id: "sa", roles: ["SUPER_ADMIN"] }), employee({ roles: ["SUPER_ADMIN"] })] }, "GET /employees/e2/grants": { grants: [] } });
    renderPage(<EmployeeDetailPage />, { path: "/employees/e2", pattern: "/employees/:id", session: hr });
    await userEvent.click(await screen.findByRole("button", { name: "İşten çıkış" }));
    expect(dialog("Deniz Kara · işten çıkış").queryByText(/Kurtarma moduna girecek/)).toBeNull();
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
    await userEvent.clear(d.getByLabelText(/Unvan/));
    await userEvent.type(d.getByLabelText(/Unvan/), "Kıdemli Analist");
    await userEvent.click(d.getByRole("button", { name: "Kaydet" }));
    await waitFor(() => expect(calls.find((c) => c.method === "PUT")?.body).toEqual({ version: 5, managerId: "m1", department: "Ürün", jobTitle: "Kıdemli Analist" }));
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
      "POST /employees/e2/rehire": () => refuse ? { status: 409, body: { error: "owns_tools" } } : { body: { employee: employee(), temporaryPassword: "Rehire-Pass-99" } },
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
    expect(await screen.findByText("Rehire-Pass-99")).toBeTruthy();
  });

  it("offers no actions on one's own record", async () => {
    fakeServer({ ...dir(employee({ id: "hr", fullName: "Ayşe İK" })), "GET /employees/hr/grants": { grants: [] } });
    renderPage(<EmployeeDetailPage />, { path: "/employees/hr", pattern: "/employees/:id", session: hr });
    expect(await screen.findByText(/Kendi çalışan kaydınızı değiştiremezsiniz/)).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Askıya al" })).toBeNull();
  });
});
