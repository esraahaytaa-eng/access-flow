import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import { fakeServer, principal } from "../test/server";

afterEach(() => vi.unstubAllGlobals());

const signedOut = { "GET /auth/me": () => ({ status: 401, body: { error: "authentication_required" } }) };
const demo = { accounts: [
  { key: "admin", email: "admin@accessflow.local", role: "Süper Yönetici" },
  { key: "employee", email: "deniz.kara@accessflow.local", role: "Çalışan" },
] };

describe("sign-in page", () => {
  it("is a single sign-in card without marketing text or a demo notice", async () => {
    fakeServer(signedOut);
    render(<App />);
    expect(await screen.findByRole("heading", { name: "Oturum açın" })).toBeTruthy();
    expect(screen.queryByText(/Doğru kişiye/)).toBeNull();
    expect(screen.queryByText("Demo hesapları")).toBeNull();
  });

  it("checks the fields before calling the server", async () => {
    const calls = fakeServer(signedOut);
    render(<App />);
    await userEvent.click(await screen.findByRole("button", { name: "Oturum aç" }));
    expect(screen.getByText("E-posta adresinizi girin.")).toBeTruthy();
    expect(screen.getByText("Parolanızı girin.")).toBeTruthy();
    expect(calls.some((c) => c.path === "/auth/login")).toBe(false);
  });

  it("in demo mode lists the demo accounts, fills the e-mail on click and shows no password", async () => {
    fakeServer({ ...signedOut, "GET /demo": demo });
    render(<App />);
    const list = await screen.findByRole("list", { name: "Demo hesapları" });
    expect(within(list).getByText("Süper Yönetici")).toBeTruthy();
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
    expect(await screen.findByText("Demo ortamı")).toBeTruthy();
  });

  it("marks the forced password change as demo in demo mode", async () => {
    fakeServer({ "GET /auth/me": principal({ mustChangePassword: true }), "GET /demo": demo });
    render(<App />);
    expect(await screen.findByRole("heading", { name: "Parolanızı değiştirin" })).toBeTruthy();
    expect(await screen.findByText("Demo ortamı")).toBeTruthy();
  });
});
