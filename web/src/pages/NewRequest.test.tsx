import { screen, within, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { currentPath, renderPage } from "../test/render";
import { fakeServer } from "../test/server";
import { addDays, todayIn } from "../ui/format";
import { NewRequestPage } from "./NewRequest";

afterEach(() => vi.unstubAllGlobals());

const jira = {
  id: "t1", name: "Jira", fulfillmentMethod: "API", owner: { id: "o1", fullName: "Can Owner" }, adminGroup: { id: "g1", name: "IT" },
  levels: [
    { id: "l1", name: "Kullanıcı", steps: ["MANAGER"], requestable: true, recommended: false },
    { id: "l2", name: "Yönetici", steps: ["MANAGER", "TOOL_OWNER", "INFOSEC"], requestable: true, recommended: false },
  ],
};
const routes = (extra: Record<string, unknown> = {}) => ({
  "GET /tools": { tools: [jira] },
  "GET /employees": { employees: [{ id: "m1", email: "m@x.co", fullName: "Burak Manager", department: null, jobTitle: null, managerId: null }] },
  "GET /access": { grants: [] },
  "GET /requests": { requests: [] },
  "POST /requests": () => ({ status: 201, body: { id: "r9", items: [] } }),
  ...extra,
});
const why = "Ödeme servisi göçü için gerekli.";

async function fillLine(level: string) {
  await userEvent.type(await screen.findByRole("combobox", { name: /^Araç/ }), "Jira");
  await userEvent.click(await screen.findByRole("option", { name: "Jira" }));
  await userEvent.click(screen.getByRole("radio", { name: new RegExp(`^${level}`) }));
}

describe("new access request", () => {
  it("finds a tool by typing and picks it with the keyboard", async () => {
    const tool = (id: string, name: string) => ({ ...jira, id, name, levels: [{ ...jira.levels[0], id: `${id}-l` }] });
    const retired = { ...tool("t9", "Jenkins"), levels: [{ ...jira.levels[0], id: "t9-l", requestable: false }] };
    fakeServer(routes({ "GET /tools": { tools: [jira, tool("t2", "GitHub"), tool("t3", "Jamf"), tool("t4", "Intercom"), retired] } }));
    renderPage(<NewRequestPage />);
    const box = await screen.findByRole("combobox", { name: /^Araç/ });
    await userEvent.type(box, "j");
    expect(screen.getAllByRole("option").map((o) => o.textContent)).toEqual(["Jamf", "Jira"]); // only requestable tools
    await userEvent.keyboard("{ArrowDown}{ArrowDown}{Enter}");
    expect((box as HTMLInputElement).value).toBe("Jira");
    expect(screen.queryByRole("listbox")).toBeNull();
    expect(screen.getByRole("radio", { name: /^Kullanıcı/ })).toBeTruthy();
    await userEvent.clear(box);
    await userEvent.type(box, "inter"); // a capital I still matches (Turkish lowercase makes it ı)
    expect(screen.getAllByRole("option").map((o) => o.textContent)).toEqual(["Intercom"]);
    await userEvent.clear(box);
    await userEvent.type(box, "zzz");
    expect(screen.getByText("Eşleşen araç yok")).toBeTruthy();
    await userEvent.keyboard("{Escape}");
    expect((box as HTMLInputElement).value).toBe("Jira"); // Escape keeps the chosen tool
  });

  it("submits a 30-day request for oneself and opens it", async () => {
    const calls = fakeServer(routes());
    renderPage(<NewRequestPage />, { path: "/request-new" });
    await fillLine("Kullanıcı");
    await userEvent.click(screen.getByRole("button", { name: /Süreli/ }));
    await userEvent.type(screen.getByLabelText(/neden ihtiyaç/), why);
    await userEvent.click(screen.getByRole("button", { name: "Talebi gönder" }));
    await waitFor(() => expect(currentPath).toBe("/requests/r9"));
    const body = calls.find((c) => c.method === "POST")!.body as { submissionId: string };
    expect(body).toEqual({
      submissionId: body.submissionId, beneficiaryId: null,
      items: [{ accessLevelId: "l1", justification: why, duration: { type: "TEMPORARY", expiresOn: addDays(todayIn("Europe/Istanbul"), 30) } }],
    });
    expect(body.submissionId).toMatch(/^[0-9a-f-]{36}$/);
  });

  it("validates the form before calling the server", async () => {
    const calls = fakeServer(routes());
    renderPage(<NewRequestPage />, { path: "/request-new" });
    await userEvent.click(await screen.findByRole("button", { name: "Talebi gönder" }));
    expect(screen.getByText("Araç seçin.")).toBeTruthy();
    expect(screen.getByText(/Gerekçe en az 15 karakter olmalı/)).toBeTruthy();
    expect(calls.some((c) => c.method === "POST")).toBe(false);
  });

  it("asks for a duration", async () => {
    fakeServer(routes());
    renderPage(<NewRequestPage />, { path: "/request-new" });
    await fillLine("Kullanıcı");
    await userEvent.type(screen.getByLabelText(/neden ihtiyaç/), why);
    await userEvent.click(screen.getByRole("button", { name: "Talebi gönder" }));
    expect(screen.getByText("Süre seçin.")).toBeTruthy();
  });

  it("extends a held grant through the change endpoint", async () => {
    const grant = { id: "gr1", toolId: "t1", tool: "Jira", accessLevelId: "l1", level: "Kullanıcı", status: "ACTIVE", expiresOn: "2026-12-01" };
    const calls = fakeServer(routes({ "GET /access": { grants: [grant] }, "POST /grants/gr1/change": () => ({ status: 201, body: { id: "r7", items: [] } }) }));
    renderPage(<NewRequestPage />, { path: "/request-new?tool=t1&level=l1" });
    expect(await screen.findByText("Süre uzatma talebi")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: /Kaldırılana kadar/ }));
    await userEvent.type(screen.getByLabelText(/neden ihtiyaç/), why);
    await userEvent.click(screen.getByRole("button", { name: "Talebi gönder" }));
    await waitFor(() => expect(currentPath).toBe("/requests/r7"));
    const body = calls.find((c) => c.path === "/grants/gr1/change")!.body as Record<string, unknown>;
    expect(body).toEqual({ submissionId: body.submissionId, accessLevelId: "l1", justification: why, duration: { type: "UNTIL_REVOKED", expiresOn: null } });
  });

  it("shows a server refusal under the line it concerns", async () => {
    fakeServer(routes({ "POST /requests": () => ({ status: 409, body: { error: "open_request_exists", field: "items[0].accessLevelId" } }) }));
    renderPage(<NewRequestPage />, { path: "/request-new" });
    await fillLine("Yönetici");
    await userEvent.click(screen.getByRole("button", { name: /Kaldırılana kadar/ }));
    await userEvent.type(screen.getByLabelText(/neden ihtiyaç/), why);
    await userEvent.click(screen.getByRole("button", { name: "Talebi gönder" }));
    const alert = await screen.findByRole("alert");
    expect(within(alert).getByText("Bu araç için zaten açık bir talep var.")).toBeTruthy();
  });
});

describe("starter package", () => {
  it("prefills the beneficiary's department package", async () => {
    const hr = { employee: { id: "hr", email: "hr@x.co", fullName: "Ayşe İK", roles: ["HR_MANAGER"] }, adminGroups: [], mustChangePassword: false, companyTimezone: "Europe/Istanbul" };
    const newHire = { id: "n1", email: "k@x.co", fullName: "Kaan Uslu", department: "Ürün", jobTitle: "Analist", managerId: "m1", status: "ACTIVE", roles: [], adminGroups: [], mustChangePassword: true, termination: null, version: 1, createdAt: "2026-10-29T09:00:00Z" };
    const calls = fakeServer(routes({
      "GET /employees": { employees: [newHire] },
      "GET /access/team": { grants: [] },
      "GET /starter-packages": { entries: [
        { id: "p1", department: "Ürün", jobTitle: null, accessLevelId: "l1", tool: "Jira", level: "Kullanıcı", requestable: true },
        { id: "p2", department: "Finans", jobTitle: null, accessLevelId: "l2", tool: "Jira", level: "Yönetici", requestable: true },
      ] },
    }));
    renderPage(<NewRequestPage />, { path: "/request-new?for=n1&pkg=1", session: hr as never });
    expect(await screen.findByRole("radio", { name: /^Kullanıcı/, checked: true })).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: /Kaldırılana kadar/ }));
    await userEvent.type(screen.getByLabelText(/neden ihtiyaç/), "İşe başlangıç paketi için gerekli.");
    await userEvent.click(screen.getByRole("button", { name: "Talebi gönder" }));
    await waitFor(() => expect(calls.find((c) => c.method === "POST")?.body).toMatchObject({ beneficiaryId: "n1", items: [{ accessLevelId: "l1" }] }));
  });
});
