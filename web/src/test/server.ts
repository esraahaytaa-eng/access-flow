import { vi } from "vitest";

type Handler = (body: unknown) => { status?: number; body?: unknown };

/** A fake API: routes are "METHOD /path" under /api/v1; unknown routes answer 404. */
export function fakeServer(routes: Record<string, Handler | unknown>) {
  const calls: { method: string; path: string; body: unknown }[] = [];
  vi.stubGlobal("fetch", vi.fn(async (url: string, init: RequestInit) => {
    const method = init.method ?? "GET";
    const path = url.replace("/api/v1", "");
    const body = init.body instanceof Blob ? { file: (init.body as File).name ?? "blob" } : init.body ? JSON.parse(init.body as string) : undefined;
    calls.push({ method, path, body });
    // A paged list's query string is optional in a route; calls keep the full path to assert on.
    const route = routes[`${method} ${path}`] ?? routes[`${method} ${path.split("?")[0]}`];
    const out = typeof route === "function" ? (route as Handler)(body) : route === undefined ? { status: 404, body: { error: "not_found" } } : { body: route };
    const status = out.status ?? 200;
    return new Response(status === 204 ? null : JSON.stringify(out.body ?? {}), { status });
  }));
  return calls;
}

export const principal = (over: Record<string, unknown> = {}) => ({
  employee: { id: "e1", email: "elif@example.com", fullName: "Elif Yılmaz", roles: [] },
  adminGroups: [],
  mustChangePassword: false,
  companyTimezone: "Europe/Istanbul",
  roleOffer: false,
  ...over,
});
