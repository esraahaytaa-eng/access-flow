import { describe, expect, it } from "vitest";
import { ApiError } from "../api/client";
import { errorMessage } from "./errors";

describe("errorMessage", () => {
  it("translates a known code", () => {
    expect(errorMessage(new ApiError(409, "version_conflict"))).toMatch(/başka biri/i);
  });
  it("falls back for an unknown code, never showing the raw code", () => {
    const msg = errorMessage(new ApiError(400, "brand_new_code"));
    expect(msg).not.toContain("brand_new_code");
    expect(msg.length).toBeGreaterThan(0);
  });
  it("handles non-API errors", () => {
    expect(errorMessage(new Error("boom")).length).toBeGreaterThan(0);
  });
});
