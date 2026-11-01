import { screen, within, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { grant, item, person, step } from "../test/fixtures";
import { currentPath, renderPage } from "../test/render";
import { fakeServer, principal } from "../test/server";
import { ApprovalsPage } from "./Approvals";
import { TasksPage } from "./Tasks";
import { TeamPage } from "./Team";

afterEach(() => vi.unstubAllGlobals());

const approval = (over: Record<string, unknown> = {}) => ({ step: step({ assigneeId: "e1", assigneeName: "Elif Yılmaz" }), item: item({ beneficiary: person("e2", "Deniz Kara"), submittedBy: person("e2", "Deniz Kara") }), unroutable: false, ...over });
const base = { "GET /employees": { employees: [] }, "GET /tools": { tools: [] } };

describe("approval inbox", () => {
  it("approves with the step version", async () => {
    const calls = fakeServer({ ...base, "GET /approvals": { approvals: [approval()] }, "POST /approvals/s1/approve": item({ status: "APPROVED" }) });
    renderPage(<ApprovalsPage />);
    await userEvent.click(await screen.findByText("Deniz Kara"));
    await userEvent.click(screen.getByRole("button", { name: "Onayla" }));
    await waitFor(() => expect(calls.find((c) => c.method === "POST")).toMatchObject({ path: "/approvals/s1/approve", body: { version: 3 } }));
  });

  it("reloads the step after a version conflict so a retry sends the new version", async () => {
    let version = 3;
    const calls = fakeServer({ ...base, "GET /approvals": () => ({ body: { approvals: [approval({ step: step({ assigneeId: "e1", assigneeName: "Elif Yılmaz", version }) })] } }),
      "POST /approvals/s1/approve": (b: unknown) => { if ((b as { version: number }).version !== 4) { version = 4; return { status: 409, body: { error: "version_conflict" } }; } return { body: item({ status: "APPROVED" }) }; } });
    renderPage(<ApprovalsPage />);
    await userEvent.click(await screen.findByText("Deniz Kara"));
    await userEvent.click(screen.getByRole("button", { name: "Onayla" }));
    await screen.findByText("İşlem tamamlanamadı");
    await waitFor(() => expect(calls.filter((c) => c.path === "/approvals").length).toBe(2));
    await userEvent.click(screen.getByRole("button", { name: "Onayla" }));
    await waitFor(() => expect(calls.filter((c) => c.method === "POST").map((c) => (c.body as { version: number }).version)).toEqual([3, 4]));
  });

  it("shows the GitHub account the approver decides on", async () => {
    const withAccount = approval({ item: item({ beneficiary: person("e2", "Deniz Kara"), submittedBy: person("e2", "Deniz Kara"), tool: "GitHub sandbox", account: { name: "octocat", ref: "583231" } }) });
    fakeServer({ ...base, "GET /approvals": { approvals: [withAccount] } });
    renderPage(<ApprovalsPage />);
    expect(await screen.findByText(/@octocat/)).toBeTruthy(); // in the list
    await userEvent.click(screen.getByText("Deniz Kara"));
    const drawer = screen.getByRole("dialog");
    expect(within(drawer).getByText("GitHub hesabı")).toBeTruthy();
    expect(within(drawer).getByText("@octocat · GitHub kimliği 583231")).toBeTruthy();
  });

  it("requires a reason to reject", async () => {
    const calls = fakeServer({ ...base, "GET /approvals": { approvals: [approval()] }, "POST /approvals/s1/reject": item({ status: "REJECTED" }) });
    renderPage(<ApprovalsPage />);
    await userEvent.click(await screen.findByText("Deniz Kara"));
    await userEvent.click(screen.getByRole("button", { name: "Reddet" }));
    expect(screen.getByText("Ret gerekçesi en az 5 karakter olmalı.")).toBeTruthy();
    await userEvent.type(screen.getByLabelText(/Ret gerekçesi/), "Bu seviye gerekli değil");
    await userEvent.click(screen.getByRole("button", { name: "Reddet" }));
    await waitFor(() => expect(calls.find((c) => c.method === "POST")?.body).toEqual({ version: 3, reason: "Bu seviye gerekli değil" }));
  });

  it("offers no override or reassignment to someone who approved an earlier step", async () => {
    const sa = principal({ employee: { id: "sa", email: "sa@x.co", fullName: "Okan Admin", roles: ["SUPER_ADMIN"] } });
    const earlier = step({ id: "s0", position: 1, status: "APPROVED", assigneeId: "sa", decidedBy: "sa", decidedAt: "2026-10-28T10:00:00Z" });
    const open = step({ position: 2, type: "TOOL_OWNER", assigneeId: "o1" });
    fakeServer({ ...base, "GET /approvals": { approvals: [] }, "GET /approvals/open": { approvals: [approval({ step: open, item: item({ beneficiary: person("e2", "Deniz Kara"), submittedBy: person("e2", "Deniz Kara"), steps: [earlier, open] }) })] } });
    renderPage(<ApprovalsPage />, { session: sa as never });
    await userEvent.click(await screen.findByRole("tab", { name: /Tüm bekleyen adımlar/ }));
    await userEvent.click(await screen.findByText("Deniz Kara"));
    expect(screen.queryByRole("button", { name: "Geçersiz kıl" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Yeniden ata" })).toBeNull();
  });

  it("lets a Super Admin override an open step they are not part of", async () => {
    const sa = principal({ employee: { id: "sa", email: "sa@x.co", fullName: "Okan Admin", roles: ["SUPER_ADMIN"] } });
    const open = approval({ step: step({ assigneeId: "m1" }) });
    const calls = fakeServer({ ...base, "GET /approvals": { approvals: [] }, "GET /approvals/open": { approvals: [open] }, "POST /approvals/s1/override": item() });
    renderPage(<ApprovalsPage />, { session: sa as never });
    await userEvent.click(await screen.findByRole("tab", { name: /Tüm bekleyen adımlar/ }));
    await userEvent.click(await screen.findByText("Deniz Kara"));
    expect(screen.queryByRole("button", { name: "Onayla" })).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "Geçersiz kıl" }));
    await userEvent.type(screen.getByLabelText(/Gerekçe/), "Yönetici iki hafta izinde");
    await userEvent.click(within(screen.getByRole("dialog", { name: "Onay adımı geçersiz kılınsın mı?" })).getByRole("button", { name: "Geçersiz kıl" }));
    await waitFor(() => expect(calls.find((c) => c.method === "POST")?.body).toEqual({ version: 3, reason: "Yönetici iki hafta izinde" }));
  });
});

const task = (over: Record<string, unknown> = {}) => ({
  id: "op1", grantId: "g1", type: "GRANT", status: "PENDING", method: "VENDOR", groupId: "g1", group: "IT", toolId: "t1", tool: "Figma",
  accessLevelId: "l1", level: "Editör", employee: person("e2", "Deniz Kara"), requestId: "r1", itemId: "i1", ticketRef: null, claimedBy: null,
  canAct: true, version: 1, createdAt: "2026-10-28T09:00:00Z", ...over,
});
const member = principal({ adminGroups: [{ id: "g1", name: "IT" }] });
/** GET /fulfillment-tasks: one page of the server's tab, with counts over the whole queue (PERF-004). */
const taskPage = (tasks: ReturnType<typeof task>[], total = tasks.length) => ({ tasks, total, page: 1, pageSize: 100,
  counts: { queue: tasks.filter((t) => t.status === "PENDING").length, mine: 0, others: 0, canAct: tasks.length } });

describe("fulfillment tasks", () => {
  it("claims, records the vendor ticket and confirms, each with the latest version", async () => {
    let current = task();
    const calls = fakeServer({
      "GET /fulfillment-tasks": () => ({ body: taskPage([current]) }),
      "POST /fulfillment-tasks/op1/claim": () => { current = task({ status: "IN_PROGRESS", claimedBy: person("e1", "Elif Yılmaz"), version: 2 }); return { body: current }; },
      "POST /fulfillment-tasks/op1/ticket": () => { current = task({ status: "IN_PROGRESS", claimedBy: person("e1", "Elif Yılmaz"), ticketRef: "FG-1", version: 3 }); return { body: current }; },
      "POST /fulfillment-tasks/op1/confirm": () => ({ body: task({ status: "SUCCEEDED", version: 4 }) }),
    });
    renderPage(<TasksPage />, { session: member as never });
    await userEvent.click(await screen.findByText("Deniz Kara"));
    await userEvent.click(screen.getByRole("button", { name: "Görevi üstlen" }));
    await userEvent.type(await screen.findByLabelText(/Tedarikçi talep numarası/), "FG-1");
    await userEvent.click(screen.getByRole("button", { name: "Talep numarasını kaydet" }));
    await userEvent.click(await screen.findByRole("button", { name: "Tedarikçi onayını kaydet" }));
    await waitFor(() => expect(calls.filter((c) => c.method === "POST").map((c) => [c.path, c.body])).toEqual([
      ["/fulfillment-tasks/op1/claim", { version: 1 }],
      ["/fulfillment-tasks/op1/ticket", { version: 2, ticketRef: "FG-1" }],
      ["/fulfillment-tasks/op1/confirm", { version: 3 }],
    ]));
  });

  it("claims once on a double click, shows one short toast and updates the drawer at once", async () => {
    const calls = fakeServer({
      "GET /fulfillment-tasks": taskPage([task()]),
      "POST /fulfillment-tasks/op1/claim": () => ({ body: task({ status: "IN_PROGRESS", claimedBy: person("e1", "Elif Yılmaz"), version: 2 }) }),
    });
    renderPage(<TasksPage />, { session: member as never });
    await userEvent.click(await screen.findByText("Deniz Kara"));
    await userEvent.dblClick(screen.getByRole("button", { name: "Görevi üstlen" }));
    const drawer = screen.getByRole("dialog", { name: /Erişim ver · Figma/ });
    expect(await within(drawer).findByRole("button", { name: "Talep numarasını kaydet" })).toBeTruthy();
    expect(within(drawer).getByText("Elif Yılmaz")).toBeTruthy(); // the claimer, from the claim's answer
    expect(within(drawer).queryByRole("button", { name: "Görevi üstlen" })).toBeNull();
    expect(calls.filter((c) => c.method === "POST")).toHaveLength(1);
    expect(screen.getAllByText("Görev üstlenildi")).toHaveLength(1);
    expect(drawer.contains(screen.getByText("Görev üstlenildi"))).toBe(false); // a toast, not part of the drawer
  });

  it("shows a refused claim as a closable error and keeps the drawer open", async () => {
    fakeServer({
      "GET /fulfillment-tasks": taskPage([task()]),
      "POST /fulfillment-tasks/op1/claim": () => ({ status: 409, body: { error: "task_claimed" } }),
    });
    renderPage(<TasksPage />, { session: member as never });
    await userEvent.click(await screen.findByText("Deniz Kara"));
    await userEvent.click(screen.getByRole("button", { name: "Görevi üstlen" }));
    expect(await screen.findByText("Bu görevi başka bir üye üstlendi.")).toBeTruthy();
    expect(screen.getByRole("dialog", { name: /Erişim ver · Figma/ })).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Bildirimi kapat" }));
    expect(screen.queryByText("Bu görevi başka bir üye üstlendi.")).toBeNull();
  });

  it("explains why one's own task cannot be claimed", async () => {
    fakeServer({ "GET /fulfillment-tasks": taskPage([task({ canAct: false })]) });
    renderPage(<TasksPage />, { session: member as never });
    await userEvent.click(await screen.findByText("Deniz Kara"));
    expect(screen.getByText("Bu görevi üstlenemezsiniz")).toBeTruthy();
    expect((screen.getByRole("button", { name: "Görevi üstlen" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("pages through a long queue on the server (PERF-004)", async () => {
    const calls = fakeServer({ "GET /fulfillment-tasks": taskPage([task()], 250) });
    renderPage(<TasksPage />, { session: member as never });
    expect(await screen.findByText("1–100 / 250")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Sonraki" }));
    await waitFor(() => expect(calls.some((c) => c.path === "/fulfillment-tasks?tab=queue&page=2")).toBe(true));
  });

  it("reports a failure with a reason", async () => {
    const mine = task({ method: "MANUAL", status: "IN_PROGRESS", claimedBy: person("e1", "Elif Yılmaz"), version: 5 });
    const calls = fakeServer({ "GET /fulfillment-tasks": taskPage([mine]), "POST /fulfillment-tasks/op1/fail": task({ status: "FAILED" }) });
    renderPage(<TasksPage />, { session: member as never });
    await userEvent.click(await screen.findByRole("tab", { name: /Üzerimdekiler/ }));
    await userEvent.click(await screen.findByText("Deniz Kara"));
    await userEvent.click(screen.getByRole("button", { name: "Başarısız bildir" }));
    await userEvent.type(screen.getByLabelText(/Ne oldu/), "Lisans kalmadı");
    await userEvent.click(within(screen.getByRole("dialog", { name: "İşlem başarısız olarak bildirilsin mi?" })).getByRole("button", { name: "Başarısız bildir" }));
    await waitFor(() => expect(calls.find((c) => c.method === "POST")?.body).toEqual({ version: 5, reason: "Lisans kalmadı" }));
  });
});

const report = { id: "e2", fullName: "Deniz Kara", department: "Ürün", jobTitle: "Analist", active: 1, expiringSoon: 0 };
/** GET /access/team: one page of direct reports, their access counted on the server (PERF-004). */
const teamPage = (members: typeof report[], total = members.length) => ({ members, total, page: 1, pageSize: 100 });

describe("team", () => {
  it("offers only a request on the report's behalf, no starter-package shortcut", async () => {
    fakeServer({ "GET /tools": { tools: [] }, "GET /approvals": { approvals: [] }, "GET /access/team": teamPage([report]), "GET /access/team/e2": { grants: [] } });
    renderPage(<TeamPage />);
    await userEvent.click(await screen.findByText("Deniz Kara"));
    expect(screen.queryByRole("button", { name: /Başlangıç paketi/ })).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "Adına erişim talep et" }));
    expect(currentPath).toBe("/request-new");
  });

  it("shows reports' access and removes a grant with its version", async () => {
    const calls = fakeServer({
      "GET /tools": { tools: [] }, "GET /approvals": { approvals: [] }, "GET /access/team": teamPage([report]),
      "GET /access/team/e2": { grants: [grant({ employee: person("e2", "Deniz Kara"), version: 9 })] },
      "POST /grants/g1/revoke": grant({ status: "REVOCATION_REQUESTED" }),
    });
    renderPage(<TeamPage />);
    await userEvent.click(await screen.findByText("Deniz Kara"));
    await userEvent.click(screen.getByRole("button", { name: "Kaldır" }));
    await userEvent.type(screen.getByLabelText(/Gerekçe/), "Projeden ayrıldı");
    await userEvent.click(within(screen.getByRole("dialog", { name: "Erişim kaldırılsın mı?" })).getByRole("button", { name: "Kaldır" }));
    await waitFor(() => expect(calls.find((c) => c.method === "POST")?.body).toEqual({ version: 9, reason: "Projeden ayrıldı" }));
  });
});
