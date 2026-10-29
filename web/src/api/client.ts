// The one fetch wrapper (A7): same-origin JSON, the CSRF header on unsafe requests,
// stable error codes, and a lost session reported once to the app.

export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    readonly field?: string,
  ) {
    super(code);
  }
}

let onUnauthorized: () => void = () => {};

// setUnauthorizedHandler is called by the session provider: a 401 means signed out.
export function setUnauthorizedHandler(handler: () => void) {
  onUnauthorized = handler;
}

function csrfToken(): string | undefined {
  return document.cookie
    .split("; ")
    .find((c) => c.startsWith("accessflow_csrf="))
    ?.slice("accessflow_csrf=".length);
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers = new Headers({ Accept: "application/json" });
  if (method !== "GET") {
    const token = csrfToken();
    if (token) headers.set("X-CSRF-Token", token);
  }
  if (body !== undefined) headers.set("Content-Type", "application/json");
  let res: Response;
  try {
    res = await fetch(`/api/v1${path}`, {
      method,
      headers,
      credentials: "same-origin",
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch {
    throw new ApiError(0, "network_error");
  }
  if (res.status === 204) return undefined as T;
  const data = await res.json().catch(() => undefined);
  if (res.ok) return data as T;
  const err = new ApiError(res.status, data?.error ?? "internal_error", data?.field);
  // A failed sign-in is not a lost session.
  if (res.status === 401 && path !== "/auth/login") onUnauthorized();
  throw err;
}

export const api = {
  get: <T>(path: string) => request<T>("GET", path),
  post: <T>(path: string, body?: unknown) => request<T>("POST", path, body),
  put: <T>(path: string, body?: unknown) => request<T>("PUT", path, body),
  del: <T>(path: string) => request<T>("DELETE", path),
};
