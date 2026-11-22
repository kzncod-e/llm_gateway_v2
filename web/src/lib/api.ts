// Browser client for the control plane. All calls go through the same-origin
// /api proxy route so the HttpOnly session cookie is sent automatically.

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

type Query = Record<string, string | number | boolean | null | undefined>;

export function withQuery(path: string, query?: Query): string {
  if (!query) return path;
  const params = new URLSearchParams();
  for (const [k, v] of Object.entries(query)) {
    if (v !== undefined && v !== null && v !== "") params.set(k, String(v));
  }
  const qs = params.toString();
  return qs ? `${path}?${qs}` : path;
}

export async function parseError(res: Response): Promise<ApiError> {
  let code = "http_error";
  let message = res.statusText || `Request failed (${res.status})`;
  try {
    const body = await res.json();
    if (body?.error) {
      code = body.error.code ?? code;
      message = body.error.message ?? message;
    }
  } catch {
    // non-JSON error body
  }
  return new ApiError(res.status, code, message);
}

export async function api<T>(
  path: string,
  init: Omit<RequestInit, "body"> & { body?: unknown; query?: Query } = {},
): Promise<T> {
  const { body, query, headers, ...rest } = init;
  const res = await fetch(withQuery(`/api/v1${path}`, query), {
    ...rest,
    credentials: "same-origin",
    // Mutations must be JSON: the backend rejects other content types (CSRF guard).
    headers: { "Content-Type": "application/json", Accept: "application/json", ...headers },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (res.status === 401 && typeof window !== "undefined" && !path.startsWith("/auth/login")) {
    const next = encodeURIComponent(window.location.pathname + window.location.search);
    // Full reload on purpose: drops every cached query belonging to the expired session.
    // eslint-disable-next-line @next/next/no-location-assign-relative-destination
    window.location.assign(`/login?next=${next}`);
  }
  if (!res.ok) throw await parseError(res);
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}
