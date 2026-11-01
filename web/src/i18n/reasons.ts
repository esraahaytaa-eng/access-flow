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
  REVOKED_NO_REVIEW: "Erişim incelemesinde yanıtlanmadı; kaldırıldı",
  "account of a terminated employee": "İşten ayrılan çalışanın hesabı",
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

// The GitHub adapter's failures (internal/access/github.go) as Turkish, actionable text. The
// stored error keeps only AccessFlow's own words and GitHub's status code, never GitHub's reply.
const toolErrors: [RegExp, (m: RegExpExecArray) => string][] = [
  [/has (\w+) access that AccessFlow did not grant/, (m) => `GitHub hesabında AccessFlow’un vermediği bir erişim var (${m[1]}); erişim verilmedi. Depo erişimini GitHub’da inceleyin, gerekirse düzeltip yeniden deneyin.`],
  [/pending invitation that AccessFlow did not grant/, () => "Hesabın AccessFlow dışında gönderilmiş bekleyen bir GitHub daveti var; erişim verilmedi. Daveti GitHub’da inceleyin veya iptal edip yeniden deneyin."],
  [/already managed by AccessFlow under another grant/, () => "Bu GitHub hesabı AccessFlow’da başka bir erişim kaydıyla yönetiliyor; aynı hesaba ikinci kayıt açılmaz."],
  [/invitation was declined or cancelled/, () => "GitHub daveti reddedildi veya iptal edildi. Kişiyle görüşün; yeniden denemek yeni bir davet gönderir."],
  [/invitation was not accepted within 7 days/, () => "GitHub daveti 7 gün içinde kabul edilmedi. Kişiyle görüşün; yeniden denemek yeni bir davet gönderir."],
  [/no longer exists/, () => "GitHub hesabı artık yok; AccessFlow erişimi değiştirmedi. Depo erişimini GitHub’da elle inceleyin."],
  [/was renamed from/, () => "GitHub hesabının kullanıcı adı değişmiş; AccessFlow erişimi değiştirmedi. Depo erişimini GitHub’da elle inceleyin."],
  [/changed outside AccessFlow/, () => "GitHub izni AccessFlow dışında değiştirilmiş; AccessFlow kaldırmadı. Depo erişimini GitHub’da elle inceleyin."],
  [/has no record of granting this GitHub access/, () => "AccessFlow’da bu GitHub erişimini verdiğine dair kayıt yok; erişimi GitHub’da elle inceleyin."],
  [/^github status \d+ \(rate limited\)/, () => "GitHub istek sınırına ulaşıldı. Sınır sıfırlandıktan sonra (en geç bir saat) yeniden deneyin."],
  [/^github status (4\d\d)$/, (m) => `GitHub isteği reddetti (HTTP ${m[1]}). Token izinlerini ve depo ayarlarını kontrol edip yeniden deneyin.`],
  [/^github( status 5\d\d|:)/, () => "GitHub geçici olarak yanıt vermedi. Bir süre sonra yeniden deneyin."],
];

/** Turkish explanation of a tool call's error, or null when there is none for it. */
export function toolErrorText(error: string): string | null {
  for (const [re, text] of toolErrors) {
    const m = re.exec(error);
    if (m) return text(m);
  }
  return null;
}
