import type { NextRequest } from "next/server";

// Same-origin proxy to the Go control plane (ADR-010). The upstream origin is
// fixed by BACKEND_URL and only /api/v1/* paths are reachable, so a caller can
// never redirect the session cookie to another host. Bodies are streamed in
// both directions, which keeps playground SSE responses incremental.

const BACKEND_URL = process.env.BACKEND_URL ?? "http://localhost:8080";

const FORWARD_REQUEST_HEADERS = ["accept", "content-type", "cookie", "user-agent", "x-gateway-provider", "x-forwarded-for"];
const FORWARD_RESPONSE_HEADERS = [
  "content-type",
  "cache-control",
  "retry-after",
  "x-request-id",
  "x-gateway-provider",
  "x-gateway-model",
  "x-gateway-attempts",
];

function badGateway(message: string, status = 502) {
  return Response.json({ error: { type: "server_error", code: "bad_gateway", message } }, { status });
}

async function handle(request: NextRequest, ctx: RouteContext<"/api/[...path]">) {
  const { path } = await ctx.params;
  if (path[0] !== "v1" || path.some((s) => s === "" || s === "." || s === "..")) {
    return badGateway("not found", 404);
  }
  const target = new URL(`/api/${path.map(encodeURIComponent).join("/")}`, BACKEND_URL);
  target.search = request.nextUrl.search;

  const headers = new Headers();
  for (const name of FORWARD_REQUEST_HEADERS) {
    const v = request.headers.get(name);
    if (v) headers.set(name, v);
  }

  const hasBody = request.method !== "GET" && request.method !== "HEAD";
  let upstream: Response;
  try {
    upstream = await fetch(target, {
      method: request.method,
      headers,
      body: hasBody ? request.body : undefined,
      // Required by undici when streaming a request body.
      ...(hasBody ? { duplex: "half" } : {}),
      redirect: "manual",
      cache: "no-store",
      signal: request.signal, // client disconnect cancels the upstream call
    } as RequestInit);
  } catch (err) {
    if (request.signal.aborted) return new Response(null, { status: 499 });
    console.error("backend unreachable", (err as Error).message);
    return badGateway("The gateway API is unreachable. Is the backend running?");
  }

  const out = new Headers();
  for (const name of FORWARD_RESPONSE_HEADERS) {
    const v = upstream.headers.get(name);
    if (v) out.set(name, v);
  }
  for (const cookie of upstream.headers.getSetCookie()) out.append("set-cookie", cookie);
  if (out.get("content-type")?.startsWith("text/event-stream")) {
    out.set("cache-control", "no-cache, no-transform");
    out.set("x-accel-buffering", "no");
  }
  return new Response(upstream.body, { status: upstream.status, headers: out });
}

export { handle as GET, handle as POST, handle as PUT, handle as PATCH, handle as DELETE };
