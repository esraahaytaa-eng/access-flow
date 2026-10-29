// Label dictionaries from the design (design/ prototype), keyed by the API's values.
import type { FulfillmentMethod, OperationType, RequestKind, Role, StepType } from "../api/types";

export const roleLabel: Record<Role, string> = { SUPER_ADMIN: "Süper Yönetici", INFOSEC: "Bilgi Güvenliği", HR_MANAGER: "İK Yöneticisi", AUDITOR: "Denetçi" };
export const stepLabel: Record<StepType, string> = { MANAGER: "Yönetici", TOOL_OWNER: "Araç sahibi", INFOSEC: "Bilgi güvenliği" };
export const methodLabel: Record<FulfillmentMethod, string> = { API: "API", MANUAL: "Manuel", VENDOR: "Tedarikçi" };
export const methodDesc: Record<FulfillmentMethod, string> = {
  API: "Otomatik sağlanır ve durum sorgusuyla doğrulanır.",
  MANUAL: "Yönetim grubu görevi tamamlar; sonuç beyana dayalıdır.",
  VENDOR: "Tedarikçi talep numarasıyla takip edilir; sonuç beyana dayalıdır.",
};
export const methodTitle: Record<FulfillmentMethod, string> = {
  API: "Otomatik sağlanır ve doğrulanır", VENDOR: "Tedarikçi talebiyle karşılanır", MANUAL: "Yönetim grubu tarafından elle karşılanır",
};
export const requestKindLabel: Record<RequestKind, string> = { NEW_ACCESS: "Yeni erişim", LEVEL_CHANGE: "Seviye değişikliği", EXTENSION: "Süre uzatma", DIRECT_GRANT: "Doğrudan erişim" };
export const operationLabel: Record<OperationType, string> = { GRANT: "Erişim ver", REVOKE: "Erişimi kaldır", SUSPEND: "Askıya al", RESUME: "Yeniden etkinleştir" };
