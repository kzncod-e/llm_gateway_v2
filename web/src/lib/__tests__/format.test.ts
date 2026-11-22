import { describe, expect, it } from "vitest";

import { formatDuration, formatMoney, formatPercent, formatRelative, nanosToUsd, usdToNanos } from "../format";

describe("money", () => {
  it("round-trips USD and nanos", () => {
    expect(usdToNanos(2.5)).toBe(2_500_000_000);
    expect(nanosToUsd(1_500_000)).toBe(0.0015);
    expect(usdToNanos(0.075)).toBe(75_000_000);
  });

  it("formats with precision suited to magnitude", () => {
    expect(formatMoney(null)).toBe("—");
    expect(formatMoney(0)).toBe("$0.00");
    expect(formatMoney(12_340_000_000)).toBe("$12.34");
    expect(formatMoney(2_000_000)).toBe("$0.002");
    expect(formatMoney(271_000)).toBe("$0.000271");
    expect(formatMoney(1_234_000_000_000, { compact: true })).toBe("$1.2K");
  });
});

describe("misc formatters", () => {
  it("formats durations", () => {
    expect(formatDuration(null)).toBe("—");
    expect(formatDuration(87)).toBe("87 ms");
    expect(formatDuration(1520)).toBe("1.52 s");
    expect(formatDuration(95_000)).toBe("1m 35s");
  });
  it("formats percentages", () => {
    expect(formatPercent(0.9876)).toBe("98.8%");
    expect(formatPercent(Number.NaN)).toBe("—");
  });
  it("formats relative times", () => {
    const now = Date.parse("2026-01-01T12:00:00Z");
    expect(formatRelative(null, now)).toBe("never");
    expect(formatRelative("2026-01-01T11:59:30Z", now)).toBe("30s ago");
    expect(formatRelative("2026-01-01T09:00:00Z", now)).toBe("3h ago");
  });
});
