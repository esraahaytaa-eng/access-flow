import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { App } from "./App";
import { fakeServer, principal } from "./test/server";

afterEach(() => vi.unstubAllGlobals());

const basics = { "GET /employees": { employees: [] }, "GET /tools": { tools: [] }, "GET /approvals": { approvals: [] }, "GET /notifications": { notifications: [] }, "GET /requests": { requests: [] }, "GET /access": { grants: [] } };

describe("session gate", () => {
  it("shows the sign-in form without a session and signs in", async () => {
    let signedIn = false;
    const calls = fakeServer({
      ...basics,
      "GET /auth/me": () => (signedIn ? { body: principal() } : { status: 401, body: { error: "authentication_required" } }),
      "POST /auth/login": () => { signedIn = true; return { body: principal() }; },
    });
    render(<App />);
    await userEvent.type(await screen.findByLabelText("E-posta"), "elif@example.com");
    await userEvent.type(screen.getByLabelText("Parola"), "correct horse battery");
    await userEvent.click(screen.getByRole("button", { name: "Giriş yap" }));
    expect(await screen.findByRole("navigation")).toBeTruthy();
    expect(calls.find((c) => c.path === "/auth/login")?.body).toEqual({ email: "elif@example.com", password: "correct horse battery" });
  });

  it("shows the server's Turkish message for wrong credentials", async () => {
    fakeServer({
      "GET /auth/me": () => ({ status: 401, body: { error: "authentication_required" } }),
      "POST /auth/login": () => ({ status: 401, body: { error: "invalid_credentials" } }),
    });
    render(<App />);
    await userEvent.type(await screen.findByLabelText("E-posta"), "elif@example.com");
    await userEvent.type(screen.getByLabelText("Parola"), "wrong password!");
    await userEvent.click(screen.getByRole("button", { name: "Giriş yap" }));
    expect((await screen.findByRole("alert")).textContent).toBe("E-posta adresi veya parola hatalı.");
  });

  it("forces a password change before anything else", async () => {
    let mustChange = true;
    const calls = fakeServer({
      ...basics,
      "GET /auth/me": () => ({ body: principal({ mustChangePassword: mustChange }) }),
      "POST /auth/password": () => { mustChange = false; return { body: principal() }; },
    });
    render(<App />);
    expect(await screen.findByRole("heading", { name: "Parolanızı değiştirin" })).toBeTruthy();
    expect(screen.queryByRole("navigation")).toBeNull();
    await userEvent.type(screen.getByLabelText("Mevcut parola"), "temporary-pass-1");
    await userEvent.type(screen.getByLabelText("Yeni parola"), "a brand new passphrase");
    await userEvent.type(screen.getByLabelText("Yeni parola (tekrar)"), "a brand new passphrase");
    await userEvent.click(screen.getByRole("button", { name: "Parolayı değiştir" }));
    expect(await screen.findByRole("navigation")).toBeTruthy();
    expect(calls.find((c) => c.path === "/auth/password")?.body).toEqual({ currentPassword: "temporary-pass-1", newPassword: "a brand new passphrase" });
  });

  it("rejects a mismatched or short new password without calling the server", async () => {
    const calls = fakeServer({ "GET /auth/me": { ...principal({ mustChangePassword: true }) } });
    render(<App />);
    await userEvent.type(await screen.findByLabelText("Mevcut parola"), "temporary-pass-1");
    await userEvent.type(screen.getByLabelText("Yeni parola"), "short");
    await userEvent.type(screen.getByLabelText("Yeni parola (tekrar)"), "different");
    await userEvent.click(screen.getByRole("button", { name: "Parolayı değiştir" }));
    expect(screen.getByText("Parola 12–128 karakter olmalı.")).toBeTruthy();
    expect(screen.getByText("Parolalar eşleşmiyor.")).toBeTruthy();
    expect(calls.some((c) => c.path === "/auth/password")).toBe(false);
  });
});

describe("navigation", () => {
  it("shows an employee only the self-service pages", async () => {
    fakeServer({ ...basics, "GET /auth/me": principal() });
    render(<App />);
    const nav = await screen.findByRole("navigation");
    expect(nav.textContent).toContain("Erişimlerim");
    expect(nav.textContent).not.toContain("Çalışanlar");
    expect(nav.textContent).not.toContain("İstisnalar");
    expect(nav.textContent).not.toContain("Karşılama görevleri");
  });

  it("shows HR the employee directory and a group member the task queue", async () => {
    fakeServer({ ...basics, "GET /auth/me": principal({ employee: { id: "e1", email: "a@b.co", fullName: "Ayşe Kaya", roles: ["HR_MANAGER"] }, adminGroups: [{ id: "g1", name: "IT" }] }), "GET /fulfillment-tasks": { tasks: [] } });
    render(<App />);
    const nav = await screen.findByRole("navigation");
    expect(nav.textContent).toContain("Çalışanlar");
    expect(nav.textContent).toContain("Karşılama görevleri");
    expect(nav.textContent).not.toContain("Araç yönetimi");
  });

  it("opens the admin pages for a Super Admin only", async () => {
    window.history.pushState({}, "", "/settings");
    fakeServer({ ...basics, "GET /auth/me": principal(), "GET /admin-groups": { groups: [] } });
    const { unmount } = render(<App />);
    expect(await screen.findByText("Bu sayfaya erişiminiz yok")).toBeTruthy();
    unmount();
    fakeServer({ ...basics, "GET /auth/me": principal({ employee: { id: "sa", email: "sa@x.co", fullName: "Okan Admin", roles: ["SUPER_ADMIN"] } }), "GET /admin-groups": { groups: [] }, "GET /exceptions": { failedOperations: [], unroutableSteps: [], overlaps: [], suspensionGaps: [], securityRevocations: [], staleGrants: [] }, "GET /fulfillment-tasks": { tasks: [] } });
    render(<App />);
    expect(await screen.findByRole("heading", { name: "Yönetim ayarları" })).toBeTruthy();
    window.history.pushState({}, "", "/");
  });

  it("reports a failure to mark notifications as read", async () => {
    const n = { id: "n1", kind: "APPROVAL_ASSIGNED", entity: "approval_step", entityId: "s1", details: { tool: "Jira", level: "Kullanıcı", employee: "Deniz Kara" }, readAt: null, createdAt: "2026-10-29T08:00:00Z" };
    fakeServer({ ...basics, "GET /auth/me": principal(), "GET /notifications": { notifications: [n] }, "POST /notifications/n1/read": () => ({ status: 500, body: { error: "internal_error" } }) });
    render(<App />);
    await screen.findByRole("navigation");
    await userEvent.click(await screen.findByRole("button", { name: /Bildirimler, 1 okunmamış/ }));
    await userEvent.click(screen.getByRole("button", { name: "Tümünü okundu say" }));
    expect(await screen.findByText("İşlem tamamlanamadı")).toBeTruthy();
  });

  it("keeps the session and says so when signing out fails", async () => {
    fakeServer({ ...basics, "GET /auth/me": principal(), "POST /auth/logout": () => ({ status: 403, body: { error: "csrf_failed" } }) });
    render(<App />);
    await screen.findByRole("navigation");
    await userEvent.click(await screen.findByRole("button", { name: /Elif Yılmaz/ }));
    await userEvent.click(screen.getByRole("button", { name: "Çıkış yap" }));
    expect(await screen.findByText("Çıkış yapılamadı")).toBeTruthy();
    expect(screen.getByRole("navigation")).toBeTruthy();
  });

  it("names the company timezone in the sidebar", async () => {
    fakeServer({ ...basics, "GET /auth/me": principal({ companyTimezone: "Europe/Berlin" }) });
    render(<App />);
    expect(await screen.findByText(/Saatler: Europe\/Berlin/)).toBeTruthy();
  });
});
