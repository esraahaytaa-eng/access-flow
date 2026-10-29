// The backend records system-made closures with fixed English reasons; people type their own.
const systemReasons: Record<string, string> = {
  "access level retired": "Erişim seviyesi kullanımdan kaldırıldı",
  "tool retired": "Araç kullanımdan kaldırıldı",
  "the access has ended": "Erişim sona erdi",
  "the grant is being removed": "Erişim kaldırılıyor",
  "employee suspended": "Çalışan askıya alındı",
  "employee terminated": "Çalışanın işine son verildi",
  transferred: "Çalışan transfer edildi",
  "expired before approval": "Onaylanmadan süresi doldu",
  expired: "Süresi doldu",
  superseded: "Yeni bir işlemle geçersiz kaldı",
  "level change": "Seviye değişikliği",
};

export const reasonText = (reason: string) => systemReasons[reason] ?? reason;

// Routing notes are composed in internal/requests/rules.go, e.g. "manager inactive → manager's manager".
const notePhrases: [RegExp, string][] = [
  [/^reassigned: /, "Yeniden atandı: "],
  [/manager's manager/g, "yöneticinin yöneticisi"],
  [/tool owner/g, "araç sahibi"],
  [/\bmanager\b/g, "yönetici"],
  [/\bInfoSec\b/g, "Bilgi Güvenliği"],
  [/ is an earlier approver/g, " önceki bir onaylayıcı"],
  [/ is the submitter/g, " talebi gönderen kişi"],
  [/ is the beneficiary/g, " hak sahibi"],
  [/ inactive/g, " aktif değil"],
  [/ missing/g, " yok"],
];

/** Turkish routing note, or null for the normal route (no note worth showing). */
export function routingNoteText(note: string): string | null {
  if (["manager", "tool owner", "InfoSec"].includes(note)) return null;
  if (note in systemReasons) return systemReasons[note];
  const text = notePhrases.reduce((s, [re, tr]) => s.replace(re, tr), note);
  return text.charAt(0).toLocaleUpperCase("tr-TR") + text.slice(1);
}
