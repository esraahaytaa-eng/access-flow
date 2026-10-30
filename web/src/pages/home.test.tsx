import { screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { employee } from "../test/fixtures";
import { renderPage } from "../test/render";
import { fakeServer, principal } from "../test/server";
import { HomePage } from "./Home";

afterEach(() => vi.unstubAllGlobals());

describe("home", () => {
  it("does not warn a lone Super Admin: one is a valid setup, and the last one cannot be removed", async () => {
    const sa = principal({ employee: { id: "sa", email: "sa@x.co", fullName: "Selin Admin", roles: ["SUPER_ADMIN"] } }) as never;
    const calls = fakeServer({ "GET /employees": { employees: [employee({ id: "sa", fullName: "Selin Admin", roles: ["SUPER_ADMIN"] })] },
      "GET /requests": { requests: [] }, "GET /access": { grants: [] } });
    renderPage(<HomePage />, { session: sa });
    await waitFor(() => expect(calls.some((c) => c.path === "/employees")).toBe(true));
    expect(await screen.findByText("planlı çıkış veya askıdaki çalışan")).toBeTruthy(); // the page rendered
    await new Promise((r) => setTimeout(r, 100)); // let the directory arrive
    expect(screen.queryByText(/Yalnızca bir aktif Süper Yönetici/)).toBeNull();
  });
});
