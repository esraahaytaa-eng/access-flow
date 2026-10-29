import { screen } from "@testing-library/react";
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
