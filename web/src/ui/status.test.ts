import { describe, expect, it } from "vitest";
import { displayBadges, evidenceBadge, statusBadge, suspensionBadge } from "./status";

describe("statusBadge", () => {
  it.each([
    ["grant", "ACTIVE", "Aktif", "green"],
    ["grant", "PENDING_FULFILLMENT", "Karşılanmayı bekliyor", "amber"],
    ["grant", "REVOCATION_FAILED", "Kaldırma başarısız", "red"],
    ["grant", "SUSPENDED", "Askıda", "amber"],
    ["item", "PENDING_APPROVAL", "Onay bekliyor", "amber"],
    ["item", "WITHDRAWN", "Geri çekildi", "outline"],
    ["step", "APPROVED_BY_SUBMISSION", "Gönderimle karşılandı", "green"],
    ["employee", "TERMINATED", "Ayrıldı", "outline"],
    ["operation", "IN_PROGRESS", "İşleniyor", "blue"],
  ] as const)("%s %s → %s", (kind, status, label, tone) => {
    expect(statusBadge(kind, status)).toEqual({ label, tone });
  });

  it("shows an unknown status verbatim instead of hiding it", () => {
    expect(statusBadge("grant", "NEW_STATE")).toEqual({ label: "NEW_STATE", tone: "outline" });
  });
});

describe("evidenceBadge", () => {
  it("always labels self-reported evidence", () => {
    expect(evidenceBadge("SELF_REPORTED")).toEqual({ label: "Beyana dayalı", tone: "violet" });
    expect(evidenceBadge("VERIFIED")).toEqual({ label: "Doğrulandı", tone: "green" });
  });
});

describe("displayBadges", () => {
  it("splits a grant display status into status and evidence badges", () => {
    expect(displayBadges("ACTIVE / SELF_REPORTED")).toEqual([
      { label: "Aktif", tone: "green" },
      { label: "Beyana dayalı", tone: "violet" },
    ]);
    expect(displayBadges("WAITING_VENDOR")).toEqual([{ label: "Tedarikçi bekleniyor", tone: "amber" }]);
    expect(displayBadges("REVOKED / VERIFIED")).toEqual([
      { label: "Kaldırıldı", tone: "outline" },
      { label: "Doğrulandı", tone: "green" },
    ]);
  });

  it("puts an unfinished suspension in place of the active status", () => {
    expect(suspensionBadge("SUSPENSION_FAILED")).toEqual({ label: "Askıya alma başarısız", tone: "red" });
    expect(suspensionBadge("RESUME_REQUESTED")).toEqual({ label: "Yeniden etkinleştiriliyor", tone: "amber" });
  });
});
