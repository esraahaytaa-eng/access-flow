import type { DisplayStatus, Evidence, Grant } from "../api/types";

export type Tone = "green" | "amber" | "red" | "blue" | "violet" | "outline";
export type Badge = { label: string; tone: Tone };
export type StatusKind = "grant" | "item" | "step" | "employee" | "operation" | "tool";

const statuses: Record<StatusKind, Record<string, Badge>> = {
  grant: {
    PROVISIONING: { label: "Sağlanıyor", tone: "blue" },
    PENDING_FULFILLMENT: { label: "Karşılanmayı bekliyor", tone: "amber" },
    WAITING_VENDOR: { label: "Tedarikçi bekleniyor", tone: "amber" },
    ACTIVE: { label: "Aktif", tone: "green" },
    FAILED: { label: "Başarısız", tone: "red" },
    SUSPENDED: { label: "Askıda", tone: "amber" },
    REVOCATION_REQUESTED: { label: "Kaldırma talep edildi", tone: "amber" },
    REVOCATION_FAILED: { label: "Kaldırma başarısız", tone: "red" },
    REVOKED: { label: "Kaldırıldı", tone: "outline" },
    CANCELLED: { label: "İptal edildi", tone: "outline" },
  },
  item: {
    PENDING_APPROVAL: { label: "Onay bekliyor", tone: "amber" },
    APPROVED: { label: "Onaylandı", tone: "blue" },
    REJECTED: { label: "Reddedildi", tone: "red" },
    WITHDRAWN: { label: "Geri çekildi", tone: "outline" },
    CANCELLED: { label: "İptal edildi", tone: "outline" },
  },
  step: {
    WAITING: { label: "Sırada", tone: "outline" },
    PENDING: { label: "Bekliyor", tone: "amber" },
    APPROVED: { label: "Onaylandı", tone: "green" },
    REJECTED: { label: "Reddedildi", tone: "red" },
    APPROVED_BY_SUBMISSION: { label: "Gönderimle karşılandı", tone: "green" },
    SUPER_ADMIN_OVERRIDE: { label: "Süper Yönetici geçersiz kıldı", tone: "violet" },
    CANCELLED: { label: "İptal edildi", tone: "outline" },
  },
  employee: {
    ACTIVE: { label: "Aktif", tone: "green" },
    SUSPENDED: { label: "Askıda", tone: "amber" },
    TERMINATED: { label: "Ayrıldı", tone: "outline" },
  },
  operation: {
    PENDING: { label: "Sırada", tone: "outline" },
    IN_PROGRESS: { label: "İşleniyor", tone: "blue" },
    SUCCEEDED: { label: "Tamamlandı", tone: "green" },
    FAILED: { label: "Başarısız", tone: "red" },
    CANCELLED: { label: "İptal edildi", tone: "outline" },
  },
  tool: {
    ACTIVE: { label: "Aktif", tone: "green" },
    RETIRED: { label: "Kullanımdan kaldırıldı", tone: "outline" },
  },
};

// One mapping for every screen (A7); an unknown value is shown rather than hidden.
export function statusBadge(kind: StatusKind, status: string): Badge {
  return statuses[kind][status] ?? { label: status, tone: "outline" };
}

export function evidenceBadge(evidence: Evidence | "IMPORTED"): Badge {
  if (evidence === "IMPORTED") return { label: "İçe aktarıldı", tone: "outline" }; // existed before AccessFlow (D10)
  return evidence === "VERIFIED"
    ? { label: "Doğrulandı", tone: "green" }
    : { label: "Beyana dayalı", tone: "violet" };
}

// "ACTIVE / SELF_REPORTED" → the status badge, then the evidence badge.
export function displayBadges(display: DisplayStatus): Badge[] {
  const [status, evidence] = display.split(" / ");
  const badges = [statusBadge("grant", status)];
  if (evidence) badges.push(evidenceBadge(evidence as Evidence));
  return badges;
}

const suspensions: Record<NonNullable<Grant["suspension"]>, Badge> = {
  SUSPENSION_REQUESTED: { label: "Askıya alma bekleniyor", tone: "amber" },
  SUSPENSION_FAILED: { label: "Askıya alma başarısız", tone: "red" },
  RESUME_REQUESTED: { label: "Yeniden etkinleştiriliyor", tone: "amber" },
  RESUME_FAILED: { label: "Yeniden etkinleştirme başarısız", tone: "red" },
};

export const suspensionBadge = (s: NonNullable<Grant["suspension"]>) => suspensions[s];
