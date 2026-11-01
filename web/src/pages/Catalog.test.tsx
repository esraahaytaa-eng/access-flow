import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { grant } from "../test/fixtures";
import { currentPath, renderPage } from "../test/render";
import { fakeServer } from "../test/server";
import { CatalogPage } from "./Catalog";

afterEach(() => vi.unstubAllGlobals());

const tool = (id: string, name: string, method: string) => ({
  id, name, fulfillmentMethod: method, owner: { id: "o1", fullName: "Can Owner" }, adminGroup: { id: "g1", name: "IT" },
  levels: [{ id: `${id}-l1`, name: "Kullanıcı", steps: ["MANAGER", "TOOL_OWNER"], requestable: true, recommended: false }],
});

describe("catalog", () => {
  it("filters by method, opens a tool and starts a request for a level", async () => {
    fakeServer({ "GET /tools": { tools: [tool("t1", "Jira", "API"), tool("t2", "Figma", "VENDOR")] }, "GET /access": { grants: [grant({ toolId: "t1", accessLevelId: "t1-l1" })] } });
    renderPage(<CatalogPage />);
    expect(await screen.findByText("Figma")).toBeTruthy();
    await userEvent.selectOptions(screen.getByLabelText("Karşılama yöntemi"), "API");
    expect(screen.queryByText("Figma")).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: /Jira/ }));
    expect(screen.getByText("Can Owner")).toBeTruthy();
    expect(screen.getByText("Sizde var")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "Süre uzat" }));
    expect(currentPath).toBe("/request-new");
  });
});

describe("catalog card: which service runs a tool", () => {
  const tools = [tool("t1", "Jira", "API"), tool("t7", "GitHub demo-project", "API"), tool("t2", "Figma", "VENDOR")];
  const card = (name: string) => within(screen.getByRole("button", { name: new RegExp(name) }));
  const real = "Gerçek GitHub API entegrasyonu aktif", mock = "Mock Tool Service kullanılıyor";

  it("shows the real GitHub API only on the tool the backend names, and only reads", async () => {
    const calls = fakeServer({ "GET /tools": { tools }, "GET /access": { grants: [] }, "GET /integrations/github": { enabled: true, toolId: "t7" } });
    renderPage(<CatalogPage />);
    await waitFor(() => expect(card("GitHub demo-project").getByText(real)).toBeTruthy());
    expect(card("Jira").getByText(mock)).toBeTruthy(); // another API tool stays on the mock
    expect(card("Jira").queryByText(real)).toBeNull();
    expect(card("Figma").queryByText(mock)).toBeNull(); // Vendor: no tool service to name
    expect(card("Figma").queryByText(real)).toBeNull();
    expect(calls.every((c) => c.method === "GET")).toBe(true);
  });

  it("shows the mock on every API tool when the adapter is off", async () => {
    fakeServer({ "GET /tools": { tools }, "GET /access": { grants: [] }, "GET /integrations/github": { enabled: false, toolId: null } });
    renderPage(<CatalogPage />);
    await waitFor(() => expect(card("GitHub demo-project").getByText(mock)).toBeTruthy());
    expect(card("Jira").getByText(mock)).toBeTruthy();
    expect(screen.queryByText(real)).toBeNull();
  });

  it("claims nothing when the status cannot be read", async () => {
    fakeServer({ "GET /tools": { tools }, "GET /access": { grants: [] }, "GET /integrations/github": () => ({ status: 500, body: { error: "internal_error" } }) });
    renderPage(<CatalogPage />);
    expect(await screen.findByText("GitHub demo-project")).toBeTruthy();
    await new Promise((r) => setTimeout(r, 50));
    expect(screen.queryByText(real)).toBeNull();
    expect(screen.queryByText(mock)).toBeNull();
  });
});
