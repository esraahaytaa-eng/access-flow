// Audit Explorer labels (design/ AUDIT_ACTION_LABEL and AUDIT_GROUPS), keyed by the API's
// action names. An action without a label is shown as its code.
export const auditActionLabel: Record<string, string> = {
  LOGIN_SUCCEEDED: "Oturum açıldı", LOGIN_FAILED: "Başarısız oturum açma", LOGIN_LOCKED: "Hesap geçici olarak kilitlendi", LOGOUT: "Oturum kapatıldı",
  PASSWORD_CHANGED: "Parola değiştirildi", PASSWORD_CHANGE_FAILED: "Parola değiştirilemedi", PASSWORD_RESET: "Geçici parola oluşturuldu",
  ACTIVATION_LINK_ISSUED: "Etkinleştirme bağlantısı gönderildi", ACTIVATION_LINK_VIEWED: "Etkinleştirme bağlantısı görüntülendi",
  ACTIVATION_COMPLETED: "Hesap etkinleştirildi", ACTIVATION_FAILED: "Etkinleştirme başarısız", MOCK_EMAIL_KEY_RESET: "Mock e-posta anahtarı sıfırlandı",
  REQUEST_SUBMITTED: "Talep gönderildi", STEP_APPROVED: "Onay adımı onaylandı", STEP_REJECTED: "Onay adımı reddedildi",
  STEP_REROUTED: "Onay adımı yeniden yönlendirildi", STEP_REASSIGNED: "Onay adımı yeniden atandı", STEP_ADDED_BY_POLICY: "Daha sıkı politika adımı eklendi",
  SUPER_ADMIN_OVERRIDE: "Süper Yönetici geçersiz kılması", APPROVAL_ATTEMPT_REJECTED: "Onay girişimi reddedildi",
  ITEM_APPROVED: "Talep kalemi onaylandı", ITEM_REJECTED: "Talep kalemi reddedildi", ITEM_WITHDRAWN: "Talep kalemi geri çekildi", ITEM_CANCELLED: "Talep kalemi iptal edildi",
  DIRECT_GRANT_CREATED: "Doğrudan erişim verildi", GRANT_CREATED: "Erişim kaydı oluşturuldu", GRANT_ACTIVATED: "Erişim etkinleşti",
  GRANT_CANCELLED: "Erişim iptal edildi", GRANT_EXTENDED: "Erişim süresi güncellendi", GRANT_REVOCATION_REQUESTED: "Erişim kaldırma talep edildi",
  GRANT_REVOKED: "Erişim kaldırıldı", GRANT_SUSPENSION_REQUESTED: "Askıya alma talep edildi", GRANT_SUSPENDED: "Erişim askıya alındı",
  GRANT_RESUME_REQUESTED: "Yeniden etkinleştirme talep edildi", GRANT_RESUMED: "Erişim yeniden etkinleşti", STALE_GRANT_INCIDENT: "Transferden sonra etkinleşen erişim",
  OPERATION_FAILED: "İşlem başarısız", OPERATION_ATTEMPT_FAILED: "İşlem denemesi başarısız", OPERATION_RETRIED: "İşlem yeniden denendi",
  INVITATION_PENDING: "Davetin kabul edilmesi bekleniyor",
  OPERATION_CANCELLED: "İşlem iptal edildi", TASK_CLAIMED: "Görev üstlenildi", TICKET_RECORDED: "Tedarikçi talep numarası kaydedildi",
  EMPLOYEE_CREATED: "Çalışan oluşturuldu", EMPLOYEE_UPDATED: "Çalışan bilgileri güncellendi", EMPLOYEE_TRANSFERRED: "Çalışan transfer edildi",
  EMPLOYEE_SUSPENDED: "Çalışan askıya alındı", EMPLOYEE_REACTIVATED: "Çalışan yeniden etkinleştirildi", EMPLOYEE_TERMINATED: "Çalışanın işine son verildi",
  EMPLOYEE_REHIRED: "Çalışan yeniden işe alındı", TERMINATION_SCHEDULED: "İşten çıkış planlandı", TERMINATION_RESCHEDULED: "Planlı işten çıkış değiştirildi",
  TERMINATION_CANCELLED: "Planlı işten çıkış iptal edildi",
  TOOL_CREATED: "Araç oluşturuldu", TOOL_UPDATED: "Araç güncellendi", TOOL_RETIRED: "Araç kullanımdan kaldırıldı",
  ACCESS_LEVEL_CREATED: "Erişim seviyesi oluşturuldu", ACCESS_LEVEL_UPDATED: "Erişim seviyesi güncellendi", ACCESS_LEVEL_RETIRED: "Erişim seviyesi kullanımdan kaldırıldı",
  STARTER_PACKAGE_ENTRY_ADDED: "Başlangıç paketine eklendi", STARTER_PACKAGE_ENTRY_REMOVED: "Başlangıç paketinden çıkarıldı",
  ROLE_ASSIGNED: "Rol atandı", ROLE_REMOVED: "Rol kaldırıldı", ADMIN_GROUP_CREATED: "Yönetim grubu oluşturuldu",
  ADMIN_GROUP_MEMBER_ADDED: "Yönetim grubuna üye eklendi", ADMIN_GROUP_MEMBER_REMOVED: "Yönetim grubundan üye çıkarıldı",
  SUPER_ADMIN_NOMINATED: "Süper Yönetici önerildi", SUPER_ADMIN_NOMINATION_AUTHORIZED: "Süper Yönetici önerisi yetkilendirildi",
  SUPER_ADMIN_NOMINATION_DECLINED: "Süper Yönetici önerisi reddedildi", SUPER_ADMIN_NOMINATION_CANCELLED: "Süper Yönetici önerisi iptal edildi",
  SUPER_ADMIN_NOMINATION_EXPIRED: "Süper Yönetici önerisinin süresi doldu",
  ADMIN_RECOVERY_REQUIRED: "Yönetici kurtarma modu başladı", SUPER_ADMIN_ROLE_LINK_ISSUED: "Süper Yönetici rol kabul bağlantısı gönderildi",
  SUPER_ADMIN_ROLE_ACCEPTED: "Süper Yönetici rolü aday tarafından kabul edildi", SUPER_ADMIN_ROLE_ACCEPT_FAILED: "Süper Yönetici rol kabul denemesi reddedildi", ADMIN_RECOVERED: "Süper Yönetici kurtarma komutuyla atandı",
  JWT_SECRET_GENERATED: "İmza anahtarı oluşturuldu",
  REVIEW_CAMPAIGN_STARTED: "Erişim denetimi başlatıldı", REVIEW_REMINDERS_SENT: "İnceleme hatırlatması gönderildi (gün 7)",
  REVIEW_ESCALATED: "İnceleme Bilgi Güvenliğine eskale edildi (gün 14)", REVIEW_KEPT: "Denetim kararı: erişim devam etsin", REVIEW_REVOKED: "Denetim kararı: erişimi kaldır",
  REVIEW_AUTO_REVOKED: "Yanıtlanmadığı için kaldırıldı (gün 21)", REVIEW_FINDING_OPENED: "Yanıtsız kalem güvenlik bulgusu oldu (gün 21)",
  REVIEW_ITEM_CLOSED: "İnceleme kalemi kapandı", REVIEW_ITEM_REROUTED: "İnceleme kalemi yeni araç sahibine yönlendirildi", REVIEW_CAMPAIGN_CLOSED: "Erişim denetimi kapandı",
  ACCESS_IMPORTED: "Mevcut erişimler içe aktarıldı", GRANT_IMPORTED: "Mevcut erişim içe aktarıldı",
  GRANT_VERIFIED_BY_TOOL: "İçe aktarılan erişim araçtan doğrulandı",
  DEPARTMENT_CREATED: "Departman tanımlandı", DEPARTMENT_RETIRED: "Departman kullanımdan kaldırıldı",
  JOB_POSITION_CREATED: "Pozisyon tanımlandı", JOB_POSITION_RETIRED: "Pozisyon kullanımdan kaldırıldı", ORPHAN_DISMISSED: "Sahipsiz hesap bulgusu kapatıldı",
  SWEEP_RAN: "Zamanlanmış işler çalıştı",
};

export const auditActionText = (action: string) => auditActionLabel[action] ?? action;

export const auditGroups: { value: string; label: string; actions: string[] }[] = [
  { value: "requests", label: "Talepler ve onaylar", actions: ["REQUEST_SUBMITTED", "STEP_APPROVED", "STEP_REJECTED", "STEP_REROUTED", "STEP_REASSIGNED",
    "STEP_ADDED_BY_POLICY", "SUPER_ADMIN_OVERRIDE", "APPROVAL_ATTEMPT_REJECTED", "ITEM_APPROVED", "ITEM_REJECTED", "ITEM_WITHDRAWN", "ITEM_CANCELLED"] },
  { value: "fulfil", label: "Sağlama ve kaldırma", actions: ["DIRECT_GRANT_CREATED", "GRANT_CREATED", "GRANT_ACTIVATED", "GRANT_CANCELLED", "GRANT_EXTENDED",
    "GRANT_REVOCATION_REQUESTED", "GRANT_REVOKED", "GRANT_SUSPENSION_REQUESTED", "GRANT_SUSPENDED", "GRANT_RESUME_REQUESTED", "GRANT_RESUMED",
    "STALE_GRANT_INCIDENT", "OPERATION_FAILED", "OPERATION_ATTEMPT_FAILED", "OPERATION_RETRIED", "OPERATION_CANCELLED", "INVITATION_PENDING", "TASK_CLAIMED", "TICKET_RECORDED"] },
  { value: "people", label: "Çalışan yaşam döngüsü", actions: ["EMPLOYEE_CREATED", "EMPLOYEE_UPDATED", "EMPLOYEE_TRANSFERRED", "EMPLOYEE_SUSPENDED",
    "EMPLOYEE_REACTIVATED", "EMPLOYEE_TERMINATED", "EMPLOYEE_REHIRED", "TERMINATION_SCHEDULED", "TERMINATION_RESCHEDULED", "TERMINATION_CANCELLED",
    "PASSWORD_RESET", "ACTIVATION_LINK_ISSUED", "ACTIVATION_LINK_VIEWED", "ACTIVATION_COMPLETED", "ACTIVATION_FAILED", "MOCK_EMAIL_KEY_RESET",
    "PASSWORD_CHANGED", "LOGIN_SUCCEEDED", "LOGIN_FAILED", "LOGIN_LOCKED", "LOGOUT"] },
  { value: "config", label: "Yapılandırma ve roller", actions: ["TOOL_CREATED", "TOOL_UPDATED", "TOOL_RETIRED", "ACCESS_LEVEL_CREATED", "ACCESS_LEVEL_UPDATED",
    "ACCESS_LEVEL_RETIRED", "STARTER_PACKAGE_ENTRY_ADDED", "STARTER_PACKAGE_ENTRY_REMOVED", "ROLE_ASSIGNED", "ROLE_REMOVED", "ADMIN_GROUP_CREATED",
    "DEPARTMENT_CREATED", "DEPARTMENT_RETIRED", "JOB_POSITION_CREATED", "JOB_POSITION_RETIRED", "ADMIN_GROUP_MEMBER_ADDED", "ADMIN_GROUP_MEMBER_REMOVED", "SUPER_ADMIN_NOMINATED", "SUPER_ADMIN_NOMINATION_AUTHORIZED",
    "SUPER_ADMIN_NOMINATION_DECLINED", "SUPER_ADMIN_NOMINATION_CANCELLED", "SUPER_ADMIN_NOMINATION_EXPIRED", "ADMIN_RECOVERY_REQUIRED", "ADMIN_RECOVERED",
    "SUPER_ADMIN_ROLE_LINK_ISSUED", "SUPER_ADMIN_ROLE_ACCEPTED", "SUPER_ADMIN_ROLE_ACCEPT_FAILED"] },
  { value: "reviews", label: "İncelemeler ve bulgular", actions: ["REVIEW_CAMPAIGN_STARTED", "REVIEW_REMINDERS_SENT", "REVIEW_ESCALATED", "REVIEW_KEPT",
    "REVIEW_REVOKED", "REVIEW_AUTO_REVOKED", "REVIEW_FINDING_OPENED", "REVIEW_ITEM_CLOSED", "REVIEW_ITEM_REROUTED", "REVIEW_CAMPAIGN_CLOSED", "ACCESS_IMPORTED", "GRANT_IMPORTED",
    "GRANT_VERIFIED_BY_TOOL", "ORPHAN_DISMISSED"] },
];

// detailsText flattens an event's details for the table and the CSV: "key: value · …".
export function detailsText(details: Record<string, unknown> | null | undefined): string {
  if (!details) return "";
  return Object.entries(details)
    .filter(([, v]) => v !== null && v !== undefined && v !== "")
    .map(([k, v]) => `${k}: ${typeof v === "object" ? JSON.stringify(v) : String(v)}`)
    .join(" · ");
}
