import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import { fakeServer, principal } from "../test/server";

afterEach(() => vi.unstubAllGlobals());

const signedOut = { "GET /auth/me": () => ({ status: 401, body: { error: "authentication_required" } }) };
const demo = { accounts: [
  { key: "admin", email: "admin@accessflow.local", role: "Super Admin" },
  { key: "employee", email: "deniz.kara@accessflow.local", role: "Employee" },
] };

describe("sign-in page", () => {
  it("is a single sign-in card without marketing text or a demo notice", async () => {
    fakeServer(signedOut);
    render(<App />);
    expect(await screen.findByRole("heading", { name: "Giriş yap" })).toBeTruthy();
    expect(screen.queryByText(/Doğru kişiye/)).toBeNull();
    expect(screen.queryByText("Demo Accounts")).toBeNull();
  });

  it("names the product through the logo", async () => {
    fakeServer(signedOut);
    render(<App />);
    expect(await screen.findByRole("img", { name: "AccessFlow" })).toBeTruthy();
  });

  it("shows and hides the password on request", async () => {
    fakeServer(signedOut);
    render(<App />);
    const field = await screen.findByLabelText("Parola") as HTMLInputElement;
    expect(field.type).toBe("password");
    await userEvent.click(screen.getByRole("button", { name: "Parolayı göster" }));
    expect(field.type).toBe("text");
    await userEvent.click(screen.getByRole("button", { name: "Parolayı gizle" }));
    expect(field.type).toBe("password");
  });

  it("checks the fields before calling the server", async () => {
    const calls = fakeServer(signedOut);
    render(<App />);
    await userEvent.click(await screen.findByRole("button", { name: "Giriş yap" }));
    expect(screen.getByText("E-posta adresinizi girin.")).toBeTruthy();
    expect(screen.getByText("Parolanızı girin.")).toBeTruthy();
    expect(calls.some((c) => c.path === "/auth/login")).toBe(false);
  });

  it("in demo mode lists the demo accounts, fills the e-mail on click and shows no password", async () => {
    fakeServer({ ...signedOut, "GET /demo": demo });
    render(<App />);
    const list = await screen.findByRole("list", { name: "Demo Accounts" });
    expect(within(list).getByText("Super Admin")).toBeTruthy();
    expect(screen.getByText("Select an account. Passwords are listed in the README.")).toBeTruthy();
    await userEvent.click(within(list).getByRole("button", { name: /deniz\.kara@accessflow\.local/ }));
    expect((screen.getByLabelText("E-posta") as HTMLInputElement).value).toBe("deniz.kara@accessflow.local");
    expect(screen.getByLabelText("Parola")).toBe(document.activeElement);
    expect(screen.queryByText(/Demo-/)).toBeNull();
  });

  it("marks every signed-in page as demo in demo mode", async () => {
    fakeServer({
      "GET /auth/me": { ...principal() }, "GET /demo": demo, "GET /employees": { employees: [] }, "GET /tools": { tools: [] },
      "GET /approvals": { approvals: [] }, "GET /notifications": { notifications: [] }, "GET /requests": { requests: [] }, "GET /access": { grants: [] },
    });
    render(<App />);
    expect(await screen.findByText("Demo Environment")).toBeTruthy();
  });

  it("marks the forced password change as demo in demo mode", async () => {
    fakeServer({ "GET /auth/me": principal({ mustChangePassword: true }), "GET /demo": demo });
    render(<App />);
    expect(await screen.findByRole("heading", { name: "Parolanızı değiştirin" })).toBeTruthy();
    expect(await screen.findByText("Demo Environment")).toBeTruthy();
  });

  it("counts the new password in characters, as the server does (ENG-008)", async () => {
    const calls = fakeServer({ "GET /auth/me": principal({ mustChangePassword: true }), "GET /demo": demo, "POST /auth/password": principal() });
    render(<App />);
    await screen.findByRole("heading", { name: "Parolanızı değiştirin" });
    const emoji = "🔑".repeat(70); // 70 characters, 140 UTF-16 units
    await userEvent.type(screen.getByLabelText(/^Mevcut parola/), "Demo-Employee-2026");
    await userEvent.type(screen.getByLabelText(/^Yeni parola$/), emoji);
    await userEvent.type(screen.getByLabelText(/^Yeni parola \(tekrar\)/), emoji);
    await userEvent.click(screen.getByRole("button", { name: /Parolayı değiştir/ }));
    expect(screen.queryByText("Parola 12–128 karakter olmalı.")).toBeNull();
    expect(calls.some((c) => c.path === "/auth/password")).toBe(true);
  });
});
