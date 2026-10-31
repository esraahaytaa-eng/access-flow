import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api } from "../api/client";
import { useEmployees, useNominations } from "../api/queries";
import type { Employee, SuperAdminNomination } from "../api/types";
import { useSession } from "../session";
import { Badge, Button, Field, Modal, Select, useAction } from "../ui/components";
import { formatDateTime } from "../ui/format";
import { managerOptions } from "./Employees";

/** Proposes a Super Admin (D2). A Super Admin proposes someone else, authorized by a second person;
 * in a succession an HR Manager appoints someone else; once InfoSec or an active Super Admin authorizes,
 * the nominee accepts through a 24-hour link (R-2). */
export function NominateModal({ employees, succession = false, onClose }: { employees: Employee[]; succession?: boolean; onClose: () => void }) {
  const qc = useQueryClient();
  const { me } = useSession();
  const [nomineeId, setNomineeId] = useState("");
  const [error, setError] = useState<string>();
  const [run, busy] = useAction();
  const otherSuperAdmins = employees.filter((e) => e.status === "ACTIVE" && e.roles.includes("SUPER_ADMIN") && e.id !== me.id).length;
  const candidates = employees.filter((e) => !e.roles.includes("SUPER_ADMIN"));
  const submit = async () => {
    if (!nomineeId) { setError("Aday seçin."); return; }
    const r = await run(() => api.post("/super-admin-nominations", { nomineeId }), succession
      ? { success: "Atama yetkilendirmeye gönderildi", successDesc: "Yetkilendirmeden sonra adaya kabul bağlantısı gider." }
      : { success: "Öneri oluşturuldu", successDesc: "Yetkilendirme için bildirim gönderildi." });
    if (r.ok) { qc.invalidateQueries({ queryKey: ["nominations"] }); onClose(); }
  };
  return (
    <Modal open onClose={onClose} title={succession ? "Yeni Süper Yönetici ata" : "Süper Yönetici öner"}
      desc={succession ? "Bir Bilgi Güvenliği üyesi veya aktif bir Süper Yönetici yetkilendirdikten sonra seçtiğiniz çalışanın e-posta kutusuna 24 saat geçerli, tek kullanımlık bir kabul bağlantısı gönderilir. Rol, çalışan kendi hesabıyla oturum açıp kabul edene kadar etkin olmaz; parolası değişmez."
        : otherSuperAdmins ? "Başka bir Süper Yönetici yetkilendirmelidir." : "Tek Süper Yönetici sizsiniz; öneriyi bir Bilgi Güvenliği üyesi yetkilendirecek."}
      footer={<><Button onClick={onClose}>Vazgeç</Button><Button variant="primary" loading={busy} onClick={submit}>{succession ? "Yetkilendirmeye gönder" : "Öner"}</Button></>}>
      <div className="stack">
        <Field label="Aday" required error={error}><Select value={nomineeId} onChange={(e) => { setNomineeId(e.target.value); setError(undefined); }} placeholder="Seçin" options={managerOptions(candidates, me.id)} /></Field>
        <p className="hint">{succession ? "Kendinizi atayamazsınız. Ayrılan Süper Yöneticinin onayı gerekmez." : "Aday kendi önerisini yetkilendiremez. Öneriler 7 gün içinde yetkilendirilmezse sona erer."} Mevcut rol ve grup üyelikleri korunur.</p>
      </div>
    </Modal>
  );
}

/** HR's view of the last Super Admin's succession (D2): the open nomination's status, never its link, a reissue once authorized, or a new appointment. */
export function SuccessionPanel() {
  const { me, timezone } = useSession();
  const qc = useQueryClient();
  const employees = (useEmployees().data ?? []) as Employee[];
  const nominations = useNominations();
  const [open, setOpen] = useState(false);
  const [run, busy] = useAction();
  const current = nominations.data?.filter((n) => n.succession) ?? [];
  const reissue = (n: SuperAdminNomination) => run(() => api.post(`/super-admin-nominations/${n.id}/reissue`, { version: n.version }),
    { success: "Yeni kabul bağlantısı gönderildi", successDesc: "Önceki bağlantı artık çalışmaz." }).then(() => qc.invalidateQueries({ queryKey: ["nominations"] }));
  if (nominations.isPending) return null;
  return (
    <div className="stack-sm" style={{ marginTop: 10 }}>
      {current.map((n) => n.authorizedAt ? (
        <div key={n.id} className="row-wrap" style={{ gap: 8 }}>
          <Badge tone={n.status === "EXPIRED" ? "red" : "amber"} dot>{n.nominee.fullName} · {n.status === "EXPIRED" ? "bağlantının süresi doldu" : "kabul bekleniyor"}</Badge>
          {n.status !== "EXPIRED" && <span className="small muted">Bağlantı {formatDateTime(n.expiresAt, timezone)} tarihine kadar geçerli</span>}
          {n.nominee.id !== me.id && <Button size="sm" loading={busy} onClick={() => reissue(n)}>Bağlantıyı yeniden gönder</Button>}
        </div>
      ) : (
        <div key={n.id} className="row-wrap" style={{ gap: 8 }}>
          <Badge tone={n.status === "EXPIRED" ? "red" : "amber"} dot>{n.nominee.fullName} · {n.status === "EXPIRED" ? "yetkilendirilmeden süresi doldu" : "yetkilendirme bekleniyor"}</Badge>
          {n.status !== "EXPIRED" && <span className="small muted">Bir Bilgi Güvenliği üyesi veya aktif bir Süper Yönetici yetkilendirmeli</span>}
        </div>
      ))}
      {current.length === 0 && <div><Button size="sm" variant="primary" onClick={() => setOpen(true)}>Yeni Süper Yönetici ata</Button></div>}
      {open && <NominateModal employees={employees} succession onClose={() => setOpen(false)} />}
    </div>
  );
}
