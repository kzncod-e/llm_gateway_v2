import { afterEach, describe, expect, it, vi } from "vitest";

import { api, ApiError, withQuery } from "../api";

afterEach(() => vi.restoreAllMocks());

describe("withQuery", () => {
  it("drops empty values", () => {
    expect(withQuery("/requests", { status: "error", q: "", team_id: undefined, limit: 50 })).toBe("/requests?status=error&limit=50");
    expect(withQuery("/x")).toBe("/x");
  });
});

describe("api", () => {
  it("sends JSON to the same-origin proxy and parses the response", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(JSON.stringify({ ok: 1 }), { status: 200 }));
    await expect(api("/teams", { method: "POST", body: { name: "x" } })).resolves.toEqual({ ok: 1 });
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("/api/v1/teams");
    expect((init as RequestInit).headers).toMatchObject({ "Content-Type": "application/json" });
    expect((init as RequestInit).body).toBe('{"name":"x"}');
  });

  it("raises ApiError with the backend error code", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ error: { code: "budget_exceeded", message: "team month budget exhausted" } }), { status: 402 }),
    );
    const err = await api("/x").catch((e) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({ status: 402, code: "budget_exceeded", message: "team month budget exhausted" });
  });

  it("returns undefined for 204", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(null, { status: 204 }));
    await expect(api("/x", { method: "DELETE" })).resolves.toBeUndefined();
  });
});
