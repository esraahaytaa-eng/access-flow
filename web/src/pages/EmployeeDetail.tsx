import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { api } from "../api/client";
import { useCompanyCatalog, useEmployees } from "../api/queries";
import type { Employee, EmployeeResponse, Grant, GrantsResponse } from "../api/types";
import { roleLabel } from "../i18n/labels";
import { useSession } from "../session";
import {
  Badge, Button, Callout, Card, ConfirmDialog, EmptyState, Field, Input, KeyValueList, Modal, PageHeader, Person, Segmented, Select, TableSkeleton, Tag, Textarea, ToolCell, useAction,
} from "../ui/components";
import { ExpiryCell, GrantBadges } from "../ui/domain";
import { addDays, formatDate, formatDateTime, todayIn, zonedInstant } from "../ui/format";
import { statusBadge } from "../ui/status";
import { catalogOptions, CreatedResult, managerOptions } from "./Employees";
import { SuccessionPanel } from "./Succession";

/** A new link ends the current password and sessions at once (M18); the admin is told before confirming. */
export const reissueWarning = (hasPassword: boolean) => hasPassword
  ? "Bu çalışanın mevcut parolası ve tüm açık oturumları hemen geçersiz olur. Yeni bağlantı 24 saat geçerlidir ve tek kullanımlıktır; Mock Email Outbox'a gönderilir."
  : "Önceki bağlantılar geçersiz olur. Yeni bağlantı 24 saat geçerlidir ve tek kullanımlıktır; Mock Email Outbox'a gönderilir.";

type ModalName = "pw" | "transfer" | "suspend" | "reactivate" | "terminate" | "cancelTerm" | "rehire" | null;

export function EmployeeDetailPage() {
  const { id } = useParams();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const { me, timezone, hasRole } = useSession();
  const query = useEmployees();
  const employees = (query.data ?? []) as Employee[];
  const emp = employees.find((e) => e.id === id);
  const grantsQuery = useQuery({ queryKey: ["employee-grants", id], enabled: !!emp, queryFn: () => api.get<GrantsResponse>(`/employees/${id}/grants`).then((r) => r.grants) });
  const [modal, setModal] = useState<ModalName>(null);

  if (query.isPending) return <TableSkeleton />;
  if (!emp) return <div className="card"><EmptyState title="Çalışan bulunamadı" action={<Button onClick={() => navigate("/employees")}>Çalışanlara dön</Button>} /></div>;
  const grants = grantsQuery.data ?? [];
  // Transfer and termination act on the access list, so they wait until HR has seen it.
  const grantsLoaded = grantsQuery.isSuccess;
  const isSelf = emp.id === me.id;
  const manager = employees.find((e) => e.id === emp.managerId);
  const reports = employees.filter((e) => e.managerId === emp.id && e.status !== "TERMINATED");
  const badge = statusBadge("employee", emp.status);
  const refresh = () => { qc.invalidateQueries({ queryKey: ["employees"] }); qc.invalidateQueries({ queryKey: ["employee-grants", id] }); };
  const close = () => setModal(null);
  const personAction = (path: string) => async (reason: string) => { await api.post(`/employees/${emp.id}/${path}`, { version: emp.version, reason }); refresh(); };

  return (
    <>
      <PageHeader back={{ label: "Çalışanlar", onClick: () => navigate("/employees") }}
        title={<span className="row" style={{ gap: 12 }}>{emp.fullName}<Badge tone={badge.tone} dot>{badge.label}</Badge></span>}
        sub={[emp.jobTitle, emp.department].filter(Boolean).join(" · ")}
        actions={!isSelf && (emp.status === "TERMINATED" ? <Button icon="refresh" onClick={() => setModal("rehire")}>Yeniden işe al</Button> : <>
          {emp.status === "ACTIVE" && <Button icon="key" onClick={() => setModal("pw")}>Etkinleştirme bağlantısı gönder</Button>}
          {emp.status === "ACTIVE" && <Button icon="swap" disabled={!grantsLoaded} onClick={() => setModal("transfer")}>Transfer</Button>}
          {emp.status === "ACTIVE" && <Button icon="pause" onClick={() => setModal("suspend")}>Askıya al</Button>}
          {emp.status === "SUSPENDED" && (!emp.roles.includes("SUPER_ADMIN") || hasRole("SUPER_ADMIN")) && <Button icon="play" onClick={() => setModal("reactivate")}>Yeniden etkinleştir</Button>}
          {emp.termination && <Button onClick={() => setModal("cancelTerm")}>Planlı çıkışı iptal et</Button>}
          <Button variant="danger-ghost" icon="logout" disabled={!grantsLoaded} onClick={() => setModal("terminate")}>İşten çıkış</Button>
        </>)} />
      {isSelf && <div style={{ marginBottom: 16 }}><Callout>Kendi çalışan kaydınızı değiştiremezsiniz. Değişiklikleri başka bir İK Yöneticisi veya Süper Yönetici yapmalıdır.</Callout></div>}
      {emp.termination && <div style={{ marginBottom: 16 }}><Callout tone="warn" title={`Planlı işten çıkış: ${formatDateTime(emp.termination.at, timezone)}`}>{emp.termination.reason}. {new Date(emp.termination.at) > new Date() ? "Belirlenen zamanda oturum kapatılır ve tüm erişimler kaldırılır."
        // the sweep runs every minute; only the last active Super Admin's exit waits longer (D2)
        : emp.roles.includes("SUPER_ADMIN") ? "Planlanan zaman geçti: çalışan son aktif Süper Yönetici olduğu için çıkış, yeni Süper Yönetici rolü kabul edene kadar bekliyor."
          : "Planlanan zaman geçti: çıkış birkaç dakika içinde otomatik işlenecek."}</Callout></div>}
      {emp.status === "TERMINATED" && grants.length > 0 && <div style={{ marginBottom: 16 }}><Callout tone="danger" title="Kaldırılması tamamlanmamış erişimler var">Çalışan ayrıldı ancak {grants.length} erişimin kaldırılması henüz doğrulanmadı. Durum İstisnalar ekranında takip ediliyor.</Callout></div>}
      <div className="grid-side">
        <div>
          <div className="section-title">Açık erişimler ({grants.length})</div>
          {grantsQuery.isPending ? <TableSkeleton rows={3} /> : grantsQuery.isError ? <div className="card"><EmptyState icon="alert" title="Erişimler yüklenemedi" desc="Sayfayı yenileyin. Transfer ve işten çıkış, erişim listesi görülmeden yapılamaz." /></div> : grants.length === 0 ? <div className="card"><EmptyState icon="key" title="Açık erişim yok" /></div> : (
            <div className="card table-wrap">
              <table className="table">
                <thead><tr><th>Araç ve seviye</th><th>Durum</th><th>Bitiş</th></tr></thead>
                <tbody>
                  {grants.map((g) => (
                    <tr key={g.id}>
                      <td><ToolCell tool={g.tool} level={g.level} sub={g.retainedOnTransfer ? <Tag warn>Transferde korundu</Tag> : g.imported && <Tag>İçe aktarılan</Tag>} /></td>
                      <td><GrantBadges grant={g} /></td>
                      <td className="small nowrap"><ExpiryCell grant={g} /></td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
        <div className="stack">
          <Card title="Profil">
            <div className="stack">
              <Person person={emp} sub={emp.email} size="lg" />
              <KeyValueList items={[
                ["Departman", emp.department ?? "—"], ["Unvan", emp.jobTitle ?? "—"],
                ["Yönetici", manager ? <Link to={`/employees/${manager.id}`}>{manager.fullName}</Link> : "Yönetim Kurulu"],
                ["Kayıt", formatDate(emp.createdAt, timezone)],
                ["Roller", emp.roles.length ? emp.roles.map((r) => roleLabel[r]).join(", ") : "—"],
                emp.adminGroups.length > 0 && ["Yönetim grupları", emp.adminGroups.map((g) => g.name).join(", ")],
                emp.mustChangePassword && ["Parola", "İlk girişte değiştirilecek"],
              ]} />
            </div>
          </Card>
          {reports.length > 0 && <Card title="Bağlı çalışanlar"><div className="stack-sm">{reports.map((r) => <Link key={r.id} to={`/employees/${r.id}`} style={{ color: "inherit", textDecoration: "none" }}><Person person={r} sub={r.jobTitle ?? false} /></Link>)}</div></Card>}
        </div>
      </div>

      <ConfirmDialog open={modal === "suspend"} onClose={close} title={`${emp.fullName} askıya alınsın mı?`} confirmLabel="Askıya al" tone="danger" reason reasonPlaceholder="Örn. Uzun süreli izin, soruşturma"
        success="Çalışan askıya alındı" successDesc="Erişimler araçlarda devre dışı bırakılıyor." onConfirm={personAction("suspend")}>
        <ul className="small" style={{ margin: 0, paddingLeft: 18, color: "var(--ink-2)", display: "grid", gap: 4 }}>
          <li>Oturum açma hemen engellenir, açık oturumlar sonlanır.</li>
          <li>{grants.filter((g) => g.status === "ACTIVE").length} aktif erişim araçlarda askıya alınır (API: doğrulanır, manuel/tedarikçi: görev açılır).</li>
          <li>Bekleyen talepleri iptal edilir; üzerindeki onaylar başkalarına aktarılır.</li>
          <li>Yeniden etkinleştirmede süresi dolmamış erişimler geri açılır.</li>
        </ul>
      </ConfirmDialog>
      <ConfirmDialog open={modal === "reactivate"} onClose={close} title={`${emp.fullName} yeniden etkinleştirilsin mi?`} desc="Oturum açabilir; süresi dolmamış erişimleri araçlarda yeniden etkinleştirilir. İptal edilen talepler geri gelmez."
        confirmLabel="Yeniden etkinleştir" reason success="Çalışan yeniden etkinleştirildi" successDesc="Uygun erişimler yeniden etkinleştiriliyor." onConfirm={personAction("reactivate")} />
      <ConfirmDialog open={modal === "cancelTerm"} onClose={close} title="Planlı çıkış iptal edilsin mi?" desc="Çalışan aktif kalır; erişimleri değişmez."
        confirmLabel="Planlı çıkışı iptal et" reason success="Planlı çıkış iptal edildi" onConfirm={personAction("termination/cancel")} />
      <ConfirmDialog open={modal === "pw"} onClose={close} title="Yeni etkinleştirme bağlantısı gönderilsin mi?" desc={reissueWarning(emp.hasPassword)}
        confirmLabel="Bağlantı gönder" success="Bağlantı gönderildi" successDesc="Etkinleştirme bağlantısı Mock Email Outbox'a gönderildi."
        onConfirm={async () => { await api.post(`/employees/${emp.id}/password-reset`); refresh(); }} />
      {modal === "transfer" && <TransferModal emp={emp} employees={employees} grants={grants} onClose={close} onDone={refresh} />}
      {modal === "terminate" && <TerminateModal emp={emp} employees={employees} openGrants={grants.filter((g) => g.status !== "REVOCATION_REQUESTED" && g.status !== "REVOCATION_FAILED").length} onClose={close} onDone={refresh} />}
      {modal === "rehire" && <RehireModal emp={emp} employees={employees} onClose={close} onDone={refresh} />}
    </>
  );
}

function TransferModal({ emp, employees, grants, onClose, onDone }: { emp: Employee; employees: Employee[]; grants: Grant[]; onClose: () => void; onDone: () => void }) {
  const [dept, setDept] = useState(emp.department ?? "");
  const [managerId, setManagerId] = useState(emp.managerId ?? "");
  const [title, setTitle] = useState(emp.jobTitle ?? "");
  const [reason, setReason] = useState("");
  const catalog = useCompanyCatalog().data;
  const [keep, setKeep] = useState<Record<string, { keep: boolean; justification: string }>>({});
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [run, busy] = useAction();
  const reviewable = grants.filter((g) => g.status !== "REVOCATION_REQUESTED" && g.status !== "REVOCATION_FAILED");
  const teamChanged = dept.trim() !== (emp.department ?? "") || managerId !== (emp.managerId ?? "");
  const hasChange = teamChanged || title.trim() !== (emp.jobTitle ?? "");
  const kept = reviewable.filter((g) => keep[g.id]?.keep);
  const setOne = (id: string, p: Partial<{ keep: boolean; justification: string }>) => { setKeep((k) => ({ ...k, [id]: { ...(k[id] ?? { keep: false, justification: "" }), ...p } })); setErrors((e) => ({ ...e, [id]: "" })); };

  const submit = async () => {
    const next: Record<string, string> = {};
    if (!title.trim() && emp.jobTitle) next.title = "Unvan boş olamaz.";
    if (teamChanged) {
      if (reason.trim().length < 3) next.reason = "Transfer gerekçesini girin.";
      for (const g of kept) if (keep[g.id].justification.trim().length < 10) next[g.id] = "Korumak için en az 10 karakterlik gerekçe yazın.";
    }
    setErrors(next);
    if (Object.values(next).some(Boolean)) return;
    const placement = { version: emp.version, managerId: managerId || null, department: dept.trim() || null, jobTitle: title.trim() || null };
    const r = await run(
      () => teamChanged
        ? api.post(`/employees/${emp.id}/transfer`, { ...placement, reason: reason.trim(), keep: kept.map((g) => ({ grantId: g.id, justification: keep[g.id].justification.trim() })) })
        : api.put(`/employees/${emp.id}`, placement),
      teamChanged
        ? { success: "Transfer tamamlandı", successDesc: `${reviewable.length - kept.length} erişim için kaldırma başlatıldı.` }
        : { success: "Unvan güncellendi", successDesc: "Yalnızca kayıt güncellendi; erişimler değişmedi." },
    );
    if (r.ok) { onDone(); onClose(); }
  };

  return (
    <Modal open wide onClose={onClose} title={`${emp.fullName} · ekip değişikliği`} desc="Departman veya yönetici değişirse mevcut erişimler varsayılan olarak kaldırılır. Korunacak her erişim için gerekçe gerekir."
      footer={<><Button onClick={onClose}>Vazgeç</Button><Button variant="primary" loading={busy} disabled={!hasChange} onClick={submit}>{teamChanged ? "Transferi onayla" : "Kaydet"}</Button></>}>
      <div className="stack">
        <div className="form-grid">
          <Field label="Yeni departman"><Select value={dept} onChange={(e) => setDept(e.target.value)} placeholder="Departman yok" options={catalogOptions(catalog?.departments, emp.department)} /></Field>
          <Field label="Yeni yönetici"><Select value={managerId} onChange={(e) => setManagerId(e.target.value)} placeholder="Yönetici yok (en üst düzey)" options={managerOptions(employees, emp.id)} /></Field>
          <Field label="Unvan" error={errors.title} className="full"><Select value={title} invalid={!!errors.title} onChange={(e) => setTitle(e.target.value)} placeholder="Unvan seçin" options={catalogOptions(catalog?.jobPositions, emp.jobTitle)} /></Field>
        </div>
        {!teamChanged && hasChange && <Callout>Yalnızca unvan değişikliği erişimleri etkilemez; denetim kaydına işlenir.</Callout>}
        {teamChanged && (
          <>
            <Field label="Transfer gerekçesi" required error={errors.reason}><Textarea rows={2} value={reason} onChange={(e) => setReason(e.target.value)} placeholder="Örn. Platform ekibine geçiş" /></Field>
            <div>
              <div className="between" style={{ marginBottom: 10 }}>
                <div className="section-title" style={{ margin: 0 }}>Mevcut erişimler ({reviewable.length}) · {reviewable.length - kept.length} kaldırılacak, {kept.length} korunacak</div>
                {reviewable.length > 0 && <div className="row">
                  <Button size="sm" onClick={() => reviewable.forEach((g) => setOne(g.id, { keep: false }))}>Tümünü kaldır</Button>
                  <Button size="sm" onClick={() => reviewable.forEach((g) => setOne(g.id, { keep: true }))}>Tümünü koru</Button>
                </div>}
              </div>
              {reviewable.length === 0 ? <p className="muted small">Açık erişim yok.</p> : (
                <div className="stack-sm">
                  {reviewable.map((g) => (
                    <div key={g.id} className="card" style={{ padding: 12 }}>
                      <div className="between" style={{ flexWrap: "wrap" }}>
                        <ToolCell tool={g.tool} level={g.level} sub={<ExpiryCell grant={g} />} />
                        <Segmented label="Karar" value={keep[g.id]?.keep ? "keep" : "revoke"} onChange={(v) => setOne(g.id, { keep: v === "keep" })} options={[{ value: "revoke", label: "Kaldır" }, { value: "keep", label: "Koru" }]} />
                      </div>
                      {keep[g.id]?.keep && (
                        <div style={{ marginTop: 10 }}>
                          <Field label="Koruma gerekçesi" required error={errors[g.id]}>
                            <Input value={keep[g.id].justification} onChange={(e) => setOne(g.id, { justification: e.target.value })} placeholder="Örn. Ortak proje Aralık sonuna kadar sürüyor." />
                          </Field>
                        </div>
                      )}
                    </div>
                  ))}
                </div>
              )}
              <p className="hint" style={{ marginTop: 10 }}>Korunan erişimler “Transferde korundu” olarak işaretlenir, bitiş tarihini korur ve yeni yöneticiye bildirilir. Bekleyen yönetici onayları yeni yöneticiye taşınır.</p>
            </div>
          </>
        )}
      </div>
    </Modal>
  );
}

function TerminateModal({ emp, employees, openGrants, onClose, onDone }: { emp: Employee; employees: Employee[]; openGrants: number; onClose: () => void; onDone: () => void }) {
  const { timezone, hasRole } = useSession();
  const [mode, setMode] = useState<"NOW" | "SCHEDULED">("NOW");
  const [date, setDate] = useState("");
  const [why, setWhy] = useState("");
  const [errors, setErrors] = useState<{ date?: string; why?: string }>({});
  const [run, busy] = useAction();
  // D2: the last active Super Admin leaves only once a successor has accepted the role (the server enforces it).
  const lastSuperAdmin = emp.roles.includes("SUPER_ADMIN") && !employees.some((e) => e.id !== emp.id && e.status === "ACTIVE" && e.roles.includes("SUPER_ADMIN"));
  const submit = async () => {
    const next = { date: mode === "SCHEDULED" && (!date || date <= todayIn(timezone)) ? "İleri bir tarih seçin." : undefined, why: why.trim().length < 3 ? "Ayrılış nedenini girin." : undefined };
    setErrors(next);
    if (next.date || next.why) return;
    const r = await run(
      () => mode === "NOW"
        ? api.post(`/employees/${emp.id}/terminate`, { version: emp.version, reason: why.trim() })
        : api.put(`/employees/${emp.id}/termination`, { effectiveAt: zonedInstant(date, "18:00", timezone), reason: why.trim(), version: emp.version }),
      mode === "NOW" ? { success: "İşten çıkış işlendi", successDesc: `${openGrants} erişim için öncelikli kaldırma başlatıldı.` } : { success: "İşten çıkış planlandı", successDesc: "Belirlenen zamanda otomatik işlenecek." },
    );
    if (r.ok) { onDone(); onClose(); }
  };
  return (
    <Modal open onClose={onClose} title={`${emp.fullName} · işten çıkış`} desc="Bu işlem geri alınamaz. Yeniden işe alımda erişimler otomatik geri gelmez."
      footer={<><Button onClick={onClose}>Vazgeç</Button><Button variant="danger" loading={busy} disabled={lastSuperAdmin} onClick={submit}>{mode === "NOW" ? "Hemen işten çıkar" : "Çıkışı planla"}</Button></>}>
      <div className="stack">
        <Segmented label="Zamanlama" value={mode} onChange={setMode} options={[{ value: "NOW", label: "Hemen" }, { value: "SCHEDULED", label: "Planlı" }]} />
        {mode === "SCHEDULED" && (
          <Field label="Geçerlilik tarihi" required error={errors.date} hint={`Seçilen gün 18.00’de (${timezone}) otomatik işlenir.`}>
            <Input type="date" min={addDays(todayIn(timezone), 1)} value={date} onChange={(e) => setDate(e.target.value)} />
          </Field>
        )}
        <Field label="Ayrılış nedeni" required error={errors.why}><Textarea rows={2} value={why} onChange={(e) => setWhy(e.target.value)} placeholder="Örn. İstifa — son çalışma günü" /></Field>
        {lastSuperAdmin && (
          <Callout tone="danger" title="Önce yeni Süper Yönetici atanmalı">
            Bu çalışan sistemin son Süper Yöneticisidir. İşten çıkış işlemini tamamlamak için önce yeni bir Süper Yönetici atanmalıdır.
            {emp.status === "ACTIVE" && <div style={{ marginTop: 6 }}>Acil durumda çalışanı hemen <strong>askıya alabilirsiniz</strong>: oturumları kapanır, ayrıcalıklı işlem yapamaz ve araçlardaki erişimleri askıya alınır.</div>}
            {hasRole("HR_MANAGER") && <SuccessionPanel />}
          </Callout>
        )}
        <Callout tone="danger" title="Gerçekleşecekler">Oturum kapatılır; bekleyen talepler iptal edilir; üzerindeki onaylar başkalarına aktarılır; {openGrants} erişim öncelikli olarak kaldırılır ve doğrulanana kadar izlenir.</Callout>
      </div>
    </Modal>
  );
}

// Not in the design: rehiring keeps the identity and restores nothing (A5, D7).
function RehireModal({ emp, employees, onClose, onDone }: { emp: Employee; employees: Employee[]; onClose: () => void; onDone: () => void }) {
  const [form, setForm] = useState({ managerId: "", department: emp.department ?? "", jobTitle: emp.jobTitle ?? "", reason: "" });
  const [error, setError] = useState("");
  const [created, setCreated] = useState<EmployeeResponse | null>(null);
  const catalog = useCompanyCatalog().data;
  const [run, busy] = useAction();
  const set = (k: keyof typeof form) => (e: { target: { value: string } }) => setForm({ ...form, [k]: e.target.value });
  const submit = async () => {
    if (form.reason.trim().length < 3) { setError("Gerekçe girin."); return; }
    setError("");
    const r = await run(() => api.post<EmployeeResponse>(`/employees/${emp.id}/rehire`, {
      version: emp.version, managerId: form.managerId || null, department: form.department.trim() || null, jobTitle: form.jobTitle.trim() || null, reason: form.reason.trim(),
    }), { success: "Çalışan yeniden işe alındı" });
    if (r.ok) { setCreated(r.res); onDone(); }
  };
  if (created) return <CreatedResult title={`${emp.fullName} yeniden işe alındı`} employee={created.employee} onClose={onClose} />;
  return (
    <Modal open wide onClose={onClose} title={`${emp.fullName} · yeniden işe alım`} desc="Aynı kimlikle yeniden etkinleştirilir. Önceki erişimler, roller ve yönetim grupları geri gelmez."
      footer={<><Button onClick={onClose}>Vazgeç</Button><Button variant="primary" loading={busy} onClick={submit}>Yeniden işe al</Button></>}>
      <div className="form-grid">
        <Field label="Yönetici"><Select value={form.managerId} onChange={set("managerId")} placeholder="Yönetici yok (en üst düzey)" options={managerOptions(employees, emp.id)} /></Field>
        <Field label="Departman"><Select value={form.department} onChange={set("department")} placeholder="Departman yok" options={catalogOptions(catalog?.departments, emp.department)} /></Field>
        <Field label="Unvan" className="full"><Select value={form.jobTitle} onChange={set("jobTitle")} placeholder="Unvan yok" options={catalogOptions(catalog?.jobPositions, emp.jobTitle)} /></Field>
        <Field label="Gerekçe" required error={error} className="full"><Textarea rows={2} value={form.reason} onChange={set("reason")} placeholder="Örn. Aynı pozisyona geri döndü" /></Field>
      </div>
    </Modal>
  );
}
