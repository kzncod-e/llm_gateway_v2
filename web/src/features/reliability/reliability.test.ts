import { describe, expect, it } from "vitest";

import { pivotErrorRates } from "./reliability-view";

describe("pivotErrorRates", () => {
  it("builds one row per bucket with a column per provider", () => {
    const { providers, rows } = pivotErrorRates([
      { bucket: "2026-01-01T10:00:00Z", provider_slug: "openai", attempts: 10, errors: 2, p95_ms: 1 },
      { bucket: "2026-01-01T10:00:00Z", provider_slug: "anthropic", attempts: 5, errors: 0, p95_ms: 1 },
      { bucket: "2026-01-01T11:00:00Z", provider_slug: "openai", attempts: 0, errors: 0, p95_ms: 1 },
    ]);
    expect(providers).toEqual(["openai", "anthropic"]);
    expect(rows).toEqual([
      { bucket: "2026-01-01T10:00:00Z", openai: 0.2, anthropic: 0 },
      { bucket: "2026-01-01T11:00:00Z", openai: 0, anthropic: 0 },
    ]);
  });
});
