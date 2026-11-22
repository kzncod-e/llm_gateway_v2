import { NextResponse, type NextRequest } from "next/server";

// Optimistic check only: send visitors without a session cookie to /login.
// Real authorization happens in the Go control plane on every API call.
export function proxy(request: NextRequest) {
  if (request.cookies.has("gw_session")) return NextResponse.next();
  const url = request.nextUrl.clone();
  url.pathname = "/login";
  url.search = `?next=${encodeURIComponent(request.nextUrl.pathname + request.nextUrl.search)}`;
  return NextResponse.redirect(url);
}

export const config = {
  matcher: ["/((?!api/|_next/|login|favicon.ico|.*\\.(?:svg|png|ico|webp)$).*)"],
};
