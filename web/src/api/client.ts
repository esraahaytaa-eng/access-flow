// The one fetch wrapper (A7): same-origin JSON (or a file), the CSRF header on unsafe
// requests, stable error codes, and a lost session reported once to the app.

export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    readonly field?: string,
    readonly body?: Record<string, unknown>, // the whole error response, e.g. a workbook's problems
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

async function request<T>(method: string, path: string, body?: unknown, asFile = false): Promise<T> {
  const headers = new Headers({ Accept: asFile ? "*/*" : "application/json" });
  if (method !== "GET") {
    const token = csrfToken();
    if (token) headers.set("X-CSRF-Token", token);
  }
  const file = body instanceof Blob;
  if (body !== undefined) headers.set("Content-Type", file ? body.type || "application/octet-stream" : "application/json");
  let res: Response;
  try {
    res = await fetch(`/api/v1${path}`, {
      method,
      headers,
      credentials: "same-origin",
      body: body === undefined ? undefined : file ? body : JSON.stringify(body),
    });
  } catch {
    throw new ApiError(0, "network_error");
  }
  if (res.status === 204) return undefined as T;
  if (res.ok && asFile) return (await res.blob()) as T;
  const data = await res.json().catch(() => undefined);
  if (res.ok) return data as T;
  const err = new ApiError(res.status, data?.error ?? "internal_error", data?.field, data);
  // A failed sign-in is not a lost session.
  if (res.status === 401 && path !== "/auth/login") onUnauthorized();
  throw err;
}

export const api = {
  get: <T>(path: string) => request<T>("GET", path),
  post: <T>(path: string, body?: unknown) => request<T>("POST", path, body),
  put: <T>(path: string, body?: unknown) => request<T>("PUT", path, body),
  del: <T>(path: string) => request<T>("DELETE", path),
  download: (path: string) => request<Blob>("GET", path, undefined, true),
};

// saveFile hands a downloaded file to the browser.
export function saveFile(blob: Blob, name: string) {
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  a.click();
  URL.revokeObjectURL(url);
}
