import { describe, expect, it } from "vitest";
import { daysUntil, formatDate, formatDateTime, relativeTime, sinceText, zonedInstant } from "./format";

describe("company-timezone formatting", () => {
  it("formats an instant in the company timezone, not the browser's", () => {
    // 21:30 UTC is already the next day in Istanbul (UTC+3).
    expect(formatDateTime("2026-10-28T21:30:00Z", "Europe/Istanbul")).toBe("29 Eki 2026 00:30");
    expect(formatDate("2026-10-28T21:30:00Z", "Europe/Istanbul")).toBe("29 Eki 2026");
  });

  it("formats a calendar date without shifting it", () => {
    expect(formatDate("2026-11-01", "America/Los_Angeles")).toBe("1 Kas 2026");
  });

  it("renders a missing value as a dash", () => {
    expect(formatDateTime(null, "Europe/Istanbul")).toBe("—");
  });
});

describe("daysUntil", () => {
  it("counts calendar days from today in the company timezone", () => {
    // 22:00 UTC on 28 Oct is already 29 Oct in Istanbul.
    const now = new Date("2026-10-28T22:00:00Z");
    expect(daysUntil("2026-10-29", "Europe/Istanbul", now)).toBe(0);
    expect(daysUntil("2026-11-05", "Europe/Istanbul", now)).toBe(7);
    expect(daysUntil("2026-10-29", "UTC", now)).toBe(1);
  });
});

describe("relativeTime", () => {
  it("speaks Turkish for past and future", () => {
    const now = new Date("2026-10-29T12:00:00Z");
    expect(relativeTime("2026-10-29T11:59:30Z", now)).toBe("az önce");
    expect(relativeTime("2026-10-29T09:00:00Z", now)).toBe("3 saat önce");
    expect(relativeTime("2026-10-31T12:00:00Z", now)).toBe("2 gün sonra");
  });
});

describe("sinceText", () => {
  it("says how long something has waited", () => {
    const now = new Date("2026-10-29T12:00:00Z");
    expect(sinceText("2026-10-29T11:55:00Z", now)).toBe("5 dakikadır");
    expect(sinceText("2026-10-29T09:00:00Z", now)).toBe("3 saattir");
    expect(sinceText("2026-10-27T11:00:00Z", now)).toBe("2 gündür");
  });
});

describe("zonedInstant", () => {
  it("turns a wall-clock time in a timezone into an RFC 3339 instant", () => {
    expect(zonedInstant("2026-11-15", "18:00", "Europe/Istanbul")).toBe("2026-11-15T15:00:00.000Z");
    // Across a DST change: New York is UTC-4 in July, UTC-5 in December.
    expect(zonedInstant("2026-07-01", "18:00", "America/New_York")).toBe("2026-07-01T22:00:00.000Z");
    expect(zonedInstant("2026-12-01", "18:00", "America/New_York")).toBe("2026-12-01T23:00:00.000Z");
  });
});
