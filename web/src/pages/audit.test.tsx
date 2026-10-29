import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { renderPage } from "../test/render";
import { fakeServer, principal } from "../test/server";
import { todayIn } from "../ui/format";
import { AuditPage } from "./Audit";

afterEach(() => vi.unstubAllGlobals());

const auditor = principal({ employee: { id: "a1", email: "a@x.co", fullName: "Ada Denetçi", roles: ["AUDITOR"] } }) as never;
const manager = principal({ employee: { id: "m1", email: "m@x.co", fullName: "Murat Yönetici", roles: [] } }) as never;
const today = todayIn("Europe/Istanbul");
const base = { "GET /employees": { employees: [] }, "GET /tools": { tools: [] }, "GET /approvals": { approvals: [] } };
const event = (over: Record<string, unknown> = {}) => ({
  id: "ev1", at: "2026-10-20T09:00:00Z", actor: { id: "m1", fullName: "Murat Yönetici" }, action: "STEP_APPROVED", entity: "approval_step",
  entityId: "s1", subject: { id: "e2", fullName: "Deniz Kara" }, reason: null, details: { tool: "Jira" }, ...over,
});
const held = (over: Record<string, unknown> = {}) => ({
  grantId: "g1", employee: { id: "e2", fullName: "Deniz Kara" }, account: null, tool: "Jira", level: "Viewer", method: "API", state: "ACTIVE",
  evidence: "VERIFIED", confirmedBy: null, effectiveFrom: "2026-09-01T09:00:00Z", effectiveTo: null, removalEvidence: null, removedBy: null,
  imported: false, direct: null, approvers: [{ type: "MANAGER", status: "APPROVED", name: "Murat Yönetici" }], ...over,
});

describe("audit explorer", () => {
  it("lists events with Turkish labels and filters by event type", async () => {
    const calls = fakeServer({ ...base,
      "GET /audit/events?": { events: [event(), event({ id: "ev2", actor: null, action: "REVIEW_ESCALATED", subject: null, details: {} })], truncated: false },
      "GET /audit/events?action=REQUEST_SUBMITTED%2CSTEP_APPROVED%2CSTEP_REJECTED%2CSTEP_REROUTED%2CSTEP_REASSIGNED%2CSTEP_ADDED_BY_POLICY%2CSUPER_ADMIN_OVERRIDE%2CAPPROVAL_ATTEMPT_REJECTED%2CITEM_APPROVED%2CITEM_REJECTED%2CITEM_WITHDRAWN%2CITEM_CANCELLED":
        { events: [event()], truncated: false } });
    renderPage(<AuditPage />, { session: auditor });
    expect(await screen.findByText("Onay adımı onaylandı")).toBeTruthy();
    expect(screen.getByText("Sistem")).toBeTruthy();
    expect(screen.getByText("tool: Jira")).toBeTruthy();
    expect(screen.getByText(/Salt okunur erişim/)).toBeTruthy();
    expect(screen.getByRole("tab", { name: "Kanıt raporları" })).toBeTruthy();
    await userEvent.selectOptions(screen.getByRole("combobox", { name: "Olay türü" }), "requests");
    await waitFor(() => expect(calls.some((c) => c.path.startsWith("/audit/events?action=REQUEST_SUBMITTED"))).toBe(true));
  });

  it("shows point-in-time states, evidence and approvers", async () => {
    fakeServer({ ...base, [`GET /audit/access?date=${today}`]: {
      at: "2026-10-29T20:59:59Z",
      grants: [held(), held({ grantId: "g2", state: "REVOCATION_FAILED", evidence: "IMPORTED", imported: true, approvers: [] })],
      removed: [held({ grantId: "g3", tool: "Figma", state: undefined, effectiveTo: "2026-10-10T09:00:00Z", removalEvidence: "SELF_REPORTED",
        removedBy: { id: "f1", fullName: "Fatma Admin" } })],
    } });
    renderPage(<AuditPage />, { session: auditor, path: "/audit?tab=pit" });
    expect(await screen.findByText("Kaldırma başarısız")).toBeTruthy();
    expect(screen.getByText("İçe aktarıldı")).toBeTruthy();
    expect(screen.getByText("Bilinmiyor (içe aktarıldı)")).toBeTruthy();
    expect(screen.getByText("Yönetici: Murat Yönetici")).toBeTruthy();
    expect(screen.getByText("Beyana dayalı · Fatma Admin")).toBeTruthy();
  });

  it("limits a manager to their reports and hides the evidence reports", async () => {
    fakeServer({ ...base, "GET /audit/events?": { events: [event()], truncated: false } });
    renderPage(<AuditPage />, { session: manager, path: "/audit?tab=reports" });
    expect(await screen.findByText(/yalnızca şu an size bağlı/)).toBeTruthy();
    expect(screen.queryByRole("tab", { name: "Kanıt raporları" })).toBeNull();
    expect(await screen.findByText("Onay adımı onaylandı")).toBeTruthy(); // falls back to the event history
  });

  it("opens the privileged roles report for a date", async () => {
    fakeServer({ ...base, [`GET /audit/reports/privileged?date=${today}`]: { at: "2026-10-29T20:59:59Z", holders: [
      { role: "SUPER_ADMIN", group: null, employee: { id: "sa", fullName: "Okan Admin" }, since: "2026-10-01T09:00:00Z", grantedBy: null },
      { role: "ADMIN_GROUP", group: "BT Ekibi", employee: { id: "o2", fullName: "Ece Operasyon" }, since: "2026-10-02T09:00:00Z", grantedBy: { id: "sa", fullName: "Okan Admin" } },
    ] } });
    renderPage(<AuditPage />, { session: auditor, path: "/audit?tab=reports" });
    await userEvent.click(await screen.findByRole("button", { name: /Ayrıcalıklı roller/ }));
    const drawer = within(screen.getByRole("dialog", { name: "Ayrıcalıklı roller" }));
    expect(await drawer.findByText("Süper Yönetici")).toBeTruthy();
    expect(drawer.getByText("Sistem (ilk kurulum)")).toBeTruthy();
    expect(drawer.getByText("Yönetim grubu: BT Ekibi")).toBeTruthy();
    expect(drawer.getByRole("button", { name: "CSV indir" })).toBeTruthy();
  });
});
