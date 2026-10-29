import { describe, expect, it } from "vitest";
import { toCsv } from "./csv";

describe("toCsv", () => {
  it("quotes cells, escapes quotes and keeps formulas inert", () => {
    expect(toCsv([["Zaman", "Gerekçe"], ['a "b"', "=HYPERLINK(1)"], [null, -3]]).slice(1))
      .toBe('"Zaman","Gerekçe"\r\n"a ""b""","\'=HYPERLINK(1)"\r\n"","\'-3"');
  });
});
