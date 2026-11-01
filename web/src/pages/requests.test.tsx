import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { grant, item, request, step } from "../test/fixtures";
import { renderPage } from "../test/render";
import { fakeServer } from "../test/server";
import { MyAccessPage } from "./MyAccess";
import { itemBucket, MyRequestsPage } from "./MyRequests";
import { RequestDetailPage } from "./RequestDetail";

afterEach(() => vi.unstubAllGlobals());

describe("itemBucket", () => {
  it.each([
    [item(), "open"],
    [item({ status: "APPROVED", fulfillment: { grantId: "g", status: "PROVISIONING", evidence: null, closedReason: null } }), "open"],
    [item({ status: "APPROVED", fulfillment: { grantId: "g", status: "ACTIVE / VERIFIED", evidence: "VERIFIED", closedReason: null } }), "done"],
    [item({ status: "APPROVED", fulfillment: { grantId: "g", status: "FAILED", evidence: null, closedReason: null } }), "open"],
    [item({ status: "WITHDRAWN" }), "closed"],
    [item({ status: "REJECTED" }), "closed"],
  ])("%#", (it_, bucket) => expect(itemBucket(it_ as never)).toBe(bucket));
});

describe("my requests", () => {
  it("lists items with their state and filters by tab", async () => {
    fakeServer({ "GET /requests": { requests: [request([item(), item({ id: "i2", tool: "GitHub", status: "REJECTED" })])] } });
    renderPage(<MyRequestsPage />);
    expect(await screen.findByText("Jira")).toBeTruthy();
    expect(screen.getByText("GitHub")).toBeTruthy();
    await userEvent.click(screen.getByRole("tab", { name: /Reddedilen ve iptal/ }));
    expect(screen.queryByText("Jira")).toBeNull();
  });
});

describe("request detail", () => {
  it("withdraws a pending item with its version and a reason", async () => {
    const calls = fakeServer({ "GET /requests/r1": request(), "GET /employees": { employees: [] }, "POST /request-items/i1/withdraw": item({ status: "WITHDRAWN" }) });
    renderPage(<RequestDetailPage />, { path: "/requests/r1", pattern: "/requests/:id" });
    expect(await screen.findByText("Yönetici onayı bekleniyor")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Talebi geri çek" }));
    await userEvent.type(screen.getByLabelText(/Gerekçe/), "Artık gerekmiyor");
    await userEvent.click(screen.getByRole("button", { name: "Geri çek" }));
    await waitFor(() => expect(calls.find((c) => c.method === "POST")?.body).toEqual({ version: 4, reason: "Artık gerekmiyor" }));
  });

  it("shows a translated routing note and the decision", async () => {
    fakeServer({
      "GET /requests/r1": request([item({ status: "REJECTED", steps: [step({ status: "REJECTED", routingNote: "manager inactive → manager's manager", decidedBy: "m1", decidedAt: "2026-10-28T10:00:00Z", reason: "Gerekli değil" })] })]),
      "GET /employees": { employees: [] },
    });
    renderPage(<RequestDetailPage />, { path: "/requests/r1", pattern: "/requests/:id" });
    expect(await screen.findByText("Yönetici reddetti")).toBeTruthy();
    expect(screen.getByText("Yönetici aktif değil → yöneticinin yöneticisi")).toBeTruthy();
    expect(screen.getByText("Gerekli değil")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Talebi geri çek" })).toBeNull();
  });

  it("shows the GitHub account an item is for", async () => {
    fakeServer({ "GET /requests/r1": request([item({ tool: "GitHub sandbox", account: { name: "octocat", ref: "583231" } })]), "GET /employees": { employees: [] } });
    renderPage(<RequestDetailPage />, { path: "/requests/r1", pattern: "/requests/:id" });
    expect(await screen.findByText("@octocat · GitHub kimliği 583231")).toBeTruthy();
    expect(screen.getByText("GitHub hesabı")).toBeTruthy();
  });

  it("explains a request that is not found or not visible", async () => {
    fakeServer({ "GET /employees": { employees: [] } });
    renderPage(<RequestDetailPage />, { path: "/requests/nope", pattern: "/requests/:id" });
    expect(await screen.findByText("Talep bulunamadı")).toBeTruthy();
  });
});

describe("my access", () => {
  it("revokes an active grant with its version and a reason", async () => {
    const calls = fakeServer({ "GET /access": { grants: [grant()] }, "POST /grants/g1/revoke": grant({ status: "REVOCATION_REQUESTED", displayStatus: "REVOCATION_REQUESTED" }) });
    renderPage(<MyAccessPage />);
    await userEvent.click(await screen.findByRole("button", { name: "Vazgeç" }));
    await userEvent.type(screen.getByLabelText(/Neden vazgeçiyorsunuz/), "Proje bitti");
    await userEvent.click(screen.getByRole("button", { name: "Erişimden vazgeç" }));
    await waitFor(() => expect(calls.find((c) => c.method === "POST")?.body).toEqual({ version: 7, reason: "Proje bitti" }));
  });

  it("shows the GitHub account of a grant", async () => {
    fakeServer({ "GET /access": { grants: [grant({ tool: "GitHub sandbox", account: { name: "octocat", ref: "583231" } })] } });
    renderPage(<MyAccessPage />);
    expect(await screen.findByText("@octocat")).toBeTruthy();
  });

  it("tells the person to accept a pending GitHub invitation", async () => {
    fakeServer({ "GET /access": { grants: [grant({ tool: "GitHub demo-project", status: "PROVISIONING", displayStatus: "PROVISIONING", evidence: null, account: { name: "octocat", ref: "583231" } })] } });
    renderPage(<MyAccessPage />);
    await userEvent.click(await screen.findByRole("tab", { name: /İşlemde/ }));
    await userEvent.click(screen.getByText("GitHub demo-project"));
    expect(await screen.findByText(/@octocat hesabına gönderilen daveti GitHub’da kabul edin/)).toBeTruthy();
  });

  it("labels self-reported access and offers no actions on a grant in progress", async () => {
    fakeServer({ "GET /access": { grants: [grant({ method: "MANUAL", displayStatus: "ACTIVE / SELF_REPORTED", evidence: "SELF_REPORTED" }), grant({ id: "g2", tool: "Figma", status: "PENDING_FULFILLMENT", displayStatus: "WAITING_VENDOR", evidence: null })] } });
    renderPage(<MyAccessPage />);
    expect(await screen.findByText("Beyana dayalı")).toBeTruthy();
    await userEvent.click(screen.getByRole("tab", { name: /İşlemde/ }));
    expect(screen.getByText("Tedarikçi bekleniyor")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Vazgeç" })).toBeNull();
  });
});
