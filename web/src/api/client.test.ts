import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError, api, setUnauthorizedHandler } from "./client";

function respond(status: number, body?: unknown) {
  return vi.spyOn(globalThis, "fetch").mockResolvedValue(
    new Response(body === undefined ? null : JSON.stringify(body), {
      status,
      headers: body === undefined ? {} : { "Content-Type": "application/json" },
    }),
  );
}

afterEach(() => {
  document.cookie = "accessflow_csrf=; expires=Thu, 01 Jan 1970 00:00:00 GMT; path=/";
  setUnauthorizedHandler(() => {});
});

describe("api", () => {
  it("sends JSON with the CSRF cookie as X-CSRF-Token on unsafe requests", async () => {
    document.cookie = "accessflow_csrf=tok123; path=/";
    const fetch = respond(200, { ok: true });
    await expect(api.post("/requests", { a: 1 })).resolves.toEqual({ ok: true });
    const [url, init] = fetch.mock.calls[0];
    expect(url).toBe("/api/v1/requests");
    expect(init?.method).toBe("POST");
    expect(init?.credentials).toBe("same-origin");
    expect(new Headers(init?.headers).get("X-CSRF-Token")).toBe("tok123");
    expect(new Headers(init?.headers).get("Content-Type")).toBe("application/json");
    expect(init?.body).toBe(JSON.stringify({ a: 1 }));
  });

  it("sends no CSRF header or body on GET", async () => {
    document.cookie = "accessflow_csrf=tok123; path=/";
    const fetch = respond(200, []);
    await api.get("/access");
    const init = fetch.mock.calls[0][1];
    expect(new Headers(init?.headers).has("X-CSRF-Token")).toBe(false);
    expect(init?.body).toBeUndefined();
  });

  it("returns undefined for 204", async () => {
    respond(204);
    await expect(api.post("/auth/logout")).resolves.toBeUndefined();
  });

  it("raises ApiError with the code and field", async () => {
    respond(400, { error: "reason_required", field: "reason" });
    const err = await api.post("/x", {}).catch((e) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({ status: 400, code: "reason_required", field: "reason" });
  });

  it("calls the unauthorized handler on 401, then raises", async () => {
    const onUnauthorized = vi.fn();
    setUnauthorizedHandler(onUnauthorized);
    respond(401, { error: "authentication_required" });
    await expect(api.get("/access")).rejects.toMatchObject({ status: 401 });
    expect(onUnauthorized).toHaveBeenCalledOnce();
  });

  it("does not treat a failed sign-in as a lost session", async () => {
    const onUnauthorized = vi.fn();
    setUnauthorizedHandler(onUnauthorized);
    respond(401, { error: "invalid_credentials" });
    await expect(api.post("/auth/login", {})).rejects.toMatchObject({ code: "invalid_credentials" });
    expect(onUnauthorized).not.toHaveBeenCalled();
  });

  it("maps a non-JSON failure to internal_error", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response("<html>", { status: 502 }));
    await expect(api.get("/x")).rejects.toMatchObject({ status: 502, code: "internal_error" });
  });

  it("maps a network failure to network_error", async () => {
    vi.spyOn(globalThis, "fetch").mockRejectedValue(new TypeError("Failed to fetch"));
    await expect(api.get("/x")).rejects.toMatchObject({ status: 0, code: "network_error" });
  });
});
