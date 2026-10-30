import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import { employee } from "../test/fixtures";
import { renderPage } from "../test/render";
import { fakeServer, principal } from "../test/server";
import { EmployeeDetailPage } from "./EmployeeDetail";
import { SettingsPage } from "./Settings";

afterEach(() => { vi.unstubAllGlobals(); window.history.replaceState(null, "", "/"); });

const signedOut = { "GET /auth/me": () => ({ status: 401, body: { error: "authentication_required" } }) };
const sa = principal({ employee: { id: "sa", email: "sa@x.co", fullName: "Selin Admin", roles: ["SUPER_ADMIN"] } }) as never;
const hr = principal({ employee: { id: "hr", email: "hr@x.co", fullName: "Ayşe İK", roles: ["HR_MANAGER"] } }) as never;

async function setPassword(pw = "violet anchor quiet 29") {
  await userEvent.type(await screen.findByLabelText("Yeni parola"), pw);
  await userEvent.type(screen.getByLabelText("Yeni parola (tekrar)"), pw);
  await userEvent.click(screen.getByRole("button", { name: "Parolamı belirle" }));
}

describe("Şifreni Belirle page", () => {
  it("takes the token from the link, hides it from the address bar and does not sign in", async () => {
    window.history.pushState(null, "", "/sifre-belirle#tok-123");
    const calls = fakeServer({ ...signedOut, "POST /auth/activate": () => ({ status: 204 }) });
    render(<App />);
    expect(await screen.findByRole("heading", { name: "Şifreni Belirle" })).toBeTruthy();
    expect(window.location.hash).toBe("");
    await setPassword();
    expect(await screen.findByText(/Parolanız belirlendi/)).toBeTruthy();
    expect(calls.find((c) => c.path === "/auth/activate")?.body).toEqual({ token: "tok-123", newPassword: "violet anchor quiet 29" });
    expect(screen.getByRole("link", { name: "Giriş yap" }).getAttribute("href")).toBe("/");
    expect(calls.some((c) => c.path === "/auth/login")).toBe(false);
  });

  it("works while someone else is signed in in the same browser", async () => {
    window.history.pushState(null, "", "/sifre-belirle#tok-123");
    fakeServer({ "GET /auth/me": principal() });
    render(<App />);
    expect(await screen.findByRole("heading", { name: "Şifreni Belirle" })).toBeTruthy();
    expect(screen.queryByRole("navigation")).toBeNull();
  });

  it("explains an invalid link and a rejected password", async () => {
    window.history.pushState(null, "", "/sifre-belirle#tok-123");
    let answer = { status: 400, body: { error: "common_password", field: "newPassword" } };
    fakeServer({ ...signedOut, "POST /auth/activate": () => answer });
    render(<App />);
    await setPassword("Password1234");
    expect(await screen.findByText(/çok yaygın/)).toBeTruthy();
    answer = { status: 400, body: { error: "invalid_activation_link", field: "" } };
    await userEvent.click(screen.getByRole("button", { name: "Parolamı belirle" }));
    expect((await screen.findByRole("alert")).textContent).toMatch(/Bağlantı geçersiz/);
  });

  it("checks the passwords before calling the server", async () => {
    window.history.pushState(null, "", "/sifre-belirle#tok-123");
    const calls = fakeServer(signedOut);
    render(<App />);
    await setPassword("too short");
    expect(await screen.findByText("Parola 12–128 karakter olmalı.")).toBeTruthy();
    expect(calls.some((c) => c.path === "/auth/activate")).toBe(false);
  });
});

describe("activation in the admin settings", () => {
  const activationEmail = { id: "m1", kind: "ACTIVATION", recipient: { id: "e2", fullName: "Deniz Kara" }, requestId: null,
    subject: "AccessFlow hesabınızı etkinleştirin", body: "Merhaba Deniz Kara", createdAt: "2026-10-30T09:00:00Z", linkAvailable: true };

  it("reveals an activation link as a clickable link without caching it", async () => {
    const calls = fakeServer({ "GET /email-outbox": { emails: [activationEmail] }, "GET /email-outbox/m1/activation-link": { token: "tok-9" } });
    renderPage(<SettingsPage />, { session: sa });
    await userEvent.click(screen.getByRole("tab", { name: "E-posta kutusu" }));
    await userEvent.click(await screen.findByText("AccessFlow hesabınızı etkinleştirin"));
    const d = within(screen.getByRole("dialog"));
    await userEvent.click(d.getByRole("button", { name: "Bağlantıyı göster" }));
    const link = await d.findByRole("link", { name: /sifre-belirle/ });
    expect(link.getAttribute("href")).toBe(`${window.location.origin}/sifre-belirle#tok-9`);
    expect(link.getAttribute("rel")).toContain("noreferrer");
    expect(calls.filter((c) => c.path === "/email-outbox/m1/activation-link")).toHaveLength(1);
  });

  it("says when a link can no longer be shown", async () => {
    fakeServer({ "GET /email-outbox": { emails: [activationEmail] },
      "GET /email-outbox/m1/activation-link": () => ({ status: 410, body: { error: "activation_link_unavailable" } }) });
    renderPage(<SettingsPage />, { session: sa });
    await userEvent.click(screen.getByRole("tab", { name: "E-posta kutusu" }));
    await userEvent.click(await screen.findByText("AccessFlow hesabınızı etkinleştirin"));
    await userEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Bağlantıyı göster" }));
    expect(await screen.findByText(/yeni bir bağlantı gönderin/)).toBeTruthy();
  });

  it("lists activations by status and reissues after a warning", async () => {
    const row = (id: string, fullName: string, status: string) => ({ employee: { id, fullName, email: `${id}@x.co` }, status, reason: "IMPORTED",
      createdAt: "2026-10-30T09:00:00Z", expiresAt: "2026-10-31T09:00:00Z", completedAt: status === "COMPLETED" ? "2026-10-30T10:00:00Z" : null });
    const calls = fakeServer({ "GET /activations": { activations: [row("p1", "Bekleyen Kişi", "PENDING"), row("c1", "Tamam Kişi", "COMPLETED")] },
      "POST /employees/c1/password-reset": () => ({ status: 204 }) });
    renderPage(<SettingsPage />, { session: sa });
    await userEvent.click(screen.getByRole("tab", { name: "Hesap etkinleştirme" }));
    expect(await screen.findByText("Bekleyen Kişi")).toBeTruthy();
    expect(screen.queryByText("Tamam Kişi")).toBeNull(); // pending first
    await userEvent.click(screen.getByRole("button", { name: /Tümü/ }));
    await userEvent.click(within(screen.getByText("Tamam Kişi").closest("tr")!).getByRole("button", { name: "Yeni bağlantı gönder" }));
    const d = within(screen.getByRole("dialog"));
    expect(d.getByText(/mevcut parolası ve tüm açık oturumları hemen geçersiz olur/)).toBeTruthy();
    expect(calls.some((c) => c.method === "POST")).toBe(false);
    await userEvent.click(d.getByRole("button", { name: "Bağlantı gönder" }));
    await waitFor(() => expect(calls.some((c) => c.path === "/employees/c1/password-reset")).toBe(true));
  });

  it("explains a missing mock email key", async () => {
    fakeServer({ "GET /activations": { activations: [{ employee: { id: "p1", fullName: "Bekleyen Kişi", email: "p@x.co" }, status: "NO_LINK",
      reason: null, createdAt: null, expiresAt: null, completedAt: null }] },
    "POST /employees/p1/password-reset": () => ({ status: 503, body: { error: "mock_email_unavailable" } }) });
    renderPage(<SettingsPage />, { session: sa });
    await userEvent.click(screen.getByRole("tab", { name: "Hesap etkinleştirme" }));
    await userEvent.click(screen.getByRole("button", { name: /Bağlantı yok/ }));
    await userEvent.click(await screen.findByRole("button", { name: "Yeni bağlantı gönder" }));
    await userEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Bağlantı gönder" }));
    expect(await screen.findByText(/reset-mail-key/)).toBeTruthy();
  });
});

describe("employee detail", () => {
  it("warns that a new link ends the current password and sends nothing until confirmed", async () => {
    const calls = fakeServer({ "GET /employees": { employees: [employee({ hasPassword: true })] }, "GET /employees/e2/grants": { grants: [] },
      "GET /tools": { tools: [] }, "GET /company-catalog": { departments: [], jobPositions: [] }, "POST /employees/e2/password-reset": () => ({ status: 204 }) });
    renderPage(<EmployeeDetailPage />, { path: "/employees/e2", pattern: "/employees/:id", session: hr });
    await userEvent.click(await screen.findByRole("button", { name: "Etkinleştirme bağlantısı gönder" }));
    const d = within(screen.getByRole("dialog"));
    expect(d.getByText(/mevcut parolası ve tüm açık oturumları hemen geçersiz olur/)).toBeTruthy();
    expect(calls.some((c) => c.method === "POST")).toBe(false);
    await userEvent.click(d.getByRole("button", { name: "Bağlantı gönder" }));
    expect(await screen.findByText(/Mock Email Outbox/)).toBeTruthy();
    expect(calls.filter((c) => c.path === "/employees/e2/password-reset")).toHaveLength(1);
  });
});

describe("two-step verification", () => {
  it("is shown as planned and only explains that it is in development", async () => {
    const calls = fakeServer({ "GET /auth/me": principal(), "GET /notifications": { notifications: [] }, "GET /requests": { requests: [] }, "GET /access": { grants: [] },
      "GET /approvals": { approvals: [] }, "GET /tools": { tools: [] }, "GET /employees": { employees: [] } });
    render(<App />);
    await userEvent.click(await screen.findByRole("button", { name: /Elif Yılmaz/ }));
    const item = screen.getByRole("button", { name: /Google Authenticator ile iki adımlı doğrulama/ });
    expect(within(item).getByText("Planlanıyor")).toBeTruthy();
    await userEvent.click(item);
    expect(await screen.findByText("Bu özellik geliştirme aşamasındadır.")).toBeTruthy();
    expect(calls.filter((c) => c.method !== "GET")).toHaveLength(0);
  });
});
