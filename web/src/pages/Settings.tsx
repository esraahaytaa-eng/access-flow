import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api } from "../api/client";
import { useAdminGroups, useEmployees, useStarterPackages, useTools } from "../api/queries";
import type { AdminGroup, AssignRoleBody, Email, EmailOutboxResponse, Employee, Role, StarterPackage } from "../api/types";
import { roleLabel } from "../i18n/labels";
import { useSession } from "../session";
import {
  Badge, Button, Callout, Card, ConfirmDialog, EmptyState, Field, Input, Modal, PageHeader, Person, Select, TableSkeleton, Tabs, useAction,
} from "../ui/components";
import { formatDateTime, relativeTime } from "../ui/format";
import { departmentsOf, managerOptions } from "./Employees";

type Tab = "roles" | "groups" | "packages" | "mail";
const assignable: AssignRoleBody["role"][] = ["INFOSEC", "HR_MANAGER", "AUDITOR"];

export function SettingsPage() {
  const [tab, setTab] = useState<Tab>("roles");
  return (
    <>
      <PageHeader title="Yönetim ayarları" sub="Ayrıcalıklı roller, yönetim grupları, başlangıç paketleri ve e-posta kutusu." />
      <Tabs value={tab} onChange={setTab} tabs={[{ value: "roles", label: "Roller" }, { value: "groups", label: "Yönetim grupları" }, { value: "packages", label: "Başlangıç paketleri" }, { value: "mail", label: "E-posta kutusu" }]} />
      {tab === "roles" ? <RolesTab /> : tab === "groups" ? <GroupsTab /> : tab === "packages" ? <PackagesTab /> : <MailboxTab />}
    </>
  );
}

function RolesTab() {
  const qc = useQueryClient();
  const { me } = useSession();
  const query = useEmployees();
  const employees = (query.data ?? []) as Employee[];
  const [assignOpen, setAssignOpen] = useState(false);
  const [remove, setRemove] = useState<{ e: Employee; r: Role } | null>(null);
  const order: Role[] = ["SUPER_ADMIN", "INFOSEC", "HR_MANAGER", "AUDITOR"];
  const rows = order.flatMap((r) => employees.filter((e) => e.status !== "TERMINATED" && e.roles.includes(r)).map((e) => ({ e, r })));
  const superAdmins = rows.filter((x) => x.r === "SUPER_ADMIN").length;
  return (
    <div className="stack">
      {!query.isPending && superAdmins < 2 && <Callout tone="warn" title="Yalnızca bir aktif Süper Yönetici var">Süper Yönetici ayrılırsa sistem kurtarma moduna girer. İkinci bir Süper Yönetici atanması önerilir.</Callout>}
      <Card title="Ayrıcalıklı roller" sub="Kimse kendi rollerini değiştiremez. Süper Yönetici rolü bu ekrandan atanamaz." pad={false}
        actions={<Button size="sm" variant="primary" icon="plus" onClick={() => setAssignOpen(true)}>Rol ata</Button>}>
        {query.isPending ? <TableSkeleton /> : (
          <div className="table-wrap">
            <table className="table">
              <thead><tr><th>Kişi</th><th>Rol</th><th /></tr></thead>
              <tbody>
                {rows.map(({ e, r }) => (
                  <tr key={`${e.id}-${r}`}>
                    <td><Person person={e} sub={e.email} /></td>
                    <td><Badge tone={r === "SUPER_ADMIN" ? "violet" : r === "INFOSEC" ? "blue" : "outline"}>{roleLabel[r]}</Badge></td>
                    <td className="actions">{e.id !== me.id && r !== "SUPER_ADMIN" && <Button size="sm" variant="danger-ghost" onClick={() => setRemove({ e, r })}>Rolü kaldır</Button>}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
      {assignOpen && <AssignRoleModal employees={employees} onClose={() => setAssignOpen(false)} />}
      <ConfirmDialog open={!!remove} onClose={() => setRemove(null)} title="Rol kaldırılsın mı?" tone="danger" confirmLabel="Rolü kaldır" success="Rol kaldırıldı"
        desc={remove ? `${remove.e.fullName} kullanıcısının ${roleLabel[remove.r]} rolü kaldırılır. Rolün geçmiş kaydı denetim için saklanır.` : undefined}
        onConfirm={async () => { await api.del(`/employees/${remove!.e.id}/roles/${remove!.r}`); qc.invalidateQueries({ queryKey: ["employees"] }); }} />
    </div>
  );
}

function AssignRoleModal({ employees, onClose }: { employees: Employee[]; onClose: () => void }) {
  const qc = useQueryClient();
  const { me } = useSession();
  const [employeeId, setEmployeeId] = useState("");
  const [role, setRole] = useState("");
  const [errors, setErrors] = useState<{ emp?: string; role?: string }>({});
  const [run, busy] = useAction();
  const submit = async () => {
    const next = { emp: employeeId ? undefined : "Kişi seçin.", role: role ? undefined : "Rol seçin." };
    setErrors(next);
    if (next.emp || next.role) return;
    const r = await run(() => api.post(`/employees/${employeeId}/roles`, { role }), { success: "Rol atandı" });
    if (r.ok) { qc.invalidateQueries({ queryKey: ["employees"] }); onClose(); }
  };
  return (
    <Modal open onClose={onClose} title="Rol ata" desc="Süper Yönetici rolü bu ekrandan atanamaz."
      footer={<><Button onClick={onClose}>Vazgeç</Button><Button variant="primary" loading={busy} onClick={submit}>Ata</Button></>}>
      <div className="stack">
        <Field label="Kişi" required error={errors.emp}><Select value={employeeId} onChange={(e) => { setEmployeeId(e.target.value); setErrors({ ...errors, emp: undefined }); }} placeholder="Seçin" options={managerOptions(employees, me.id)} /></Field>
        <Field label="Rol" required error={errors.role}><Select value={role} onChange={(e) => { setRole(e.target.value); setErrors({ ...errors, role: undefined }); }} placeholder="Seçin" options={assignable.map((r) => ({ value: r, label: roleLabel[r] }))} /></Field>
        {role === "AUDITOR" && <Callout tone="info">Denetçi rolü salt okunurdur; onay, karşılama veya yönetim yetkisi vermez.</Callout>}
      </div>
    </Modal>
  );
}

function GroupsTab() {
  const qc = useQueryClient();
  const groups = useAdminGroups();
  const employees = (useEmployees().data ?? []) as Employee[];
  const tools = useTools().data ?? [];
  const [name, setName] = useState("");
  const [remove, setRemove] = useState<{ g: AdminGroup; e: Employee } | null>(null);
  const [run, busy] = useAction();
  const create = async () => {
    if (!name.trim()) return;
    const r = await run(() => api.post("/admin-groups", { name: name.trim() }), { success: "Grup oluşturuldu" });
    if (r.ok) { setName(""); qc.invalidateQueries({ queryKey: ["admin-groups"] }); }
  };
  return (
    <div className="stack">
      <Card title="Yeni yönetim grubu" sub="Gruplar oluşturulduktan sonra yeniden adlandırılamaz.">
        <div className="row"><Input value={name} onChange={(e) => setName(e.target.value)} placeholder="Örn. İK Sistemleri" aria-label="Grup adı" /><Button variant="primary" loading={busy} disabled={!name.trim()} onClick={create}>Oluştur</Button></div>
      </Card>
      {groups.isPending ? <TableSkeleton /> : (
        <div className="grid-2">
          {(groups.data ?? []).map((g) => {
            const names = tools.filter((t) => t.status !== "RETIRED" && t.adminGroup.id === g.id).map((t) => t.name);
            return <GroupCard key={g.id} group={g} employees={employees} sub={names.length ? `Araçlar: ${names.join(", ")}` : "Henüz araç atanmadı"} onRemove={(e) => setRemove({ g, e })} />;
          })}
        </div>
      )}
      <ConfirmDialog open={!!remove} onClose={() => setRemove(null)} title="Üye gruptan çıkarılsın mı?" tone="danger" confirmLabel="Çıkar" success="Üye çıkarıldı"
        desc={remove ? `${remove.e.fullName} artık ${remove.g.name} görevlerini üstlenemez. Üyelik geçmişi saklanır.` : undefined}
        onConfirm={async () => { await api.del(`/admin-groups/${remove!.g.id}/members/${remove!.e.id}`); qc.invalidateQueries({ queryKey: ["admin-groups"] }); }} />
    </div>
  );
}

function GroupCard({ group, employees, sub, onRemove }: { group: AdminGroup; employees: Employee[]; sub: string; onRemove: (e: Employee) => void }) {
  const qc = useQueryClient();
  const { me } = useSession();
  const [selected, setSelected] = useState("");
  const [run, busy] = useAction();
  const memberIds = group.memberIds ?? [];
  const members = memberIds.map((id) => employees.find((e) => e.id === id)).filter((e): e is Employee => !!e);
  const add = async () => {
    const r = await run(() => api.post(`/admin-groups/${group.id}/members`, { employeeId: selected }), { success: "Üye eklendi" });
    if (r.ok) { setSelected(""); qc.invalidateQueries({ queryKey: ["admin-groups"] }); }
  };
  return (
    <Card title={group.name} sub={sub}>
      <div className="stack-sm">
        {members.length === 0 && <p className="muted small">Üye yok.</p>}
        {members.map((e) => <div key={e.id} className="between"><Person person={e} />{e.id !== me.id && <Button size="sm" variant="ghost" onClick={() => onRemove(e)}>Çıkar</Button>}</div>)}
        <div className="row" style={{ marginTop: 6 }}>
          <Select aria-label="Üye ekle" value={selected} onChange={(e) => setSelected(e.target.value)} placeholder="Üye ekle…"
            options={employees.filter((e) => e.status === "ACTIVE" && e.id !== me.id && !memberIds.includes(e.id)).map((e) => ({ value: e.id, label: e.fullName }))} />
          <Button disabled={!selected} loading={busy} onClick={add}>Ekle</Button>
        </div>
      </div>
    </Card>
  );
}

function PackagesTab() {
  const qc = useQueryClient();
  const packages = useStarterPackages();
  const employees = (useEmployees().data ?? []) as Employee[];
  const tools = (useTools().data ?? []).filter((t) => t.status !== "RETIRED");
  const [form, setForm] = useState({ department: "", jobTitle: "", levelId: "" });
  const [remove, setRemove] = useState<StarterPackage | null>(null);
  const [run, busy] = useAction();
  const levelOptions = tools.flatMap((t) => t.levels.filter((l) => l.status !== "RETIRED").map((l) => ({ value: l.id, label: `${t.name} · ${l.name}` })));
  const add = async () => {
    const r = await run(() => api.post("/starter-packages", { department: form.department.trim(), jobTitle: form.jobTitle.trim() || null, accessLevelId: form.levelId }), { success: "Paket öğesi eklendi" });
    if (r.ok) { setForm({ ...form, levelId: "" }); qc.invalidateQueries({ queryKey: ["starter-packages"] }); }
  };
  const rows = [...(packages.data ?? [])].sort((a, b) => a.department.localeCompare(b.department, "tr") || (a.jobTitle ?? "").localeCompare(b.jobTitle ?? "", "tr"));
  return (
    <div className="stack">
      <Card title="Başlangıç paketine ekle" sub="Yeni çalışanlara departman ve unvana göre önerilen erişimler. Öneri bağlayıcı değildir; talepler normal onaydan geçer.">
        <div className="form-grid">
          <Field label="Departman" required><Input list="settings-departments" value={form.department} onChange={(e) => setForm({ ...form, department: e.target.value })} /></Field>
          <Field label="Unvan" hint="Boş bırakılırsa tüm departmana uygulanır."><Input value={form.jobTitle} onChange={(e) => setForm({ ...form, jobTitle: e.target.value })} /></Field>
          <Field label="Araç ve seviye" required className="full"><Select value={form.levelId} onChange={(e) => setForm({ ...form, levelId: e.target.value })} placeholder="Seçin" options={levelOptions} /></Field>
        </div>
        <datalist id="settings-departments">{departmentsOf(employees).map((d) => <option key={d} value={d} />)}</datalist>
        <div className="row" style={{ justifyContent: "flex-end", marginTop: 12 }}><Button variant="primary" loading={busy} disabled={!form.department.trim() || !form.levelId} onClick={add}>Ekle</Button></div>
      </Card>
      {packages.isPending ? <TableSkeleton /> : rows.length === 0 ? <div className="card"><EmptyState icon="box" title="Başlangıç paketi tanımlı değil" /></div> : (
        <div className="card table-wrap">
          <table className="table">
            <thead><tr><th>Departman</th><th>Unvan</th><th>Araç ve seviye</th><th /></tr></thead>
            <tbody>
              {rows.map((p) => (
                <tr key={p.id}>
                  <td>{p.department}</td>
                  <td className="small">{p.jobTitle ?? <span className="muted">Tüm departman</span>}</td>
                  <td>{p.tool} · {p.level}{!p.requestable && <> <Badge tone="outline">Kullanımdan kaldırıldı</Badge></>}</td>
                  <td className="actions"><Button size="sm" variant="danger-ghost" onClick={() => setRemove(p)}>Kaldır</Button></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <ConfirmDialog open={!!remove} onClose={() => setRemove(null)} title="Paket öğesi kaldırılsın mı?" tone="danger" confirmLabel="Kaldır" success="Paket öğesi kaldırıldı"
        desc={remove ? `${remove.department}${remove.jobTitle ? ` · ${remove.jobTitle}` : ""}: ${remove.tool} · ${remove.level}. Mevcut erişimler etkilenmez.` : undefined}
        onConfirm={async () => { await api.del(`/starter-packages/${remove!.id}`); qc.invalidateQueries({ queryKey: ["starter-packages"] }); }} />
    </div>
  );
}

function MailboxTab() {
  const { timezone } = useSession();
  const query = useQuery({ queryKey: ["email-outbox"], queryFn: () => api.get<EmailOutboxResponse>("/email-outbox").then((r) => r.emails) });
  const [open, setOpen] = useState<Email | null>(null);
  const emails = query.data ?? [];
  return (
    <div className="stack">
      <Callout tone="info" title="Simülasyon e-posta kutusu">Gerçek e-posta gönderilmez. Yönetici onayı gerektiren her talep gönderiminde yöneticiye tek bir bilgilendirme e-postası düşer. E-posta üzerinden onay verilemez.</Callout>
      {query.isPending ? <TableSkeleton /> : emails.length === 0 ? <div className="card"><EmptyState icon="mail" title="E-posta yok" /></div> : (
        <div className="card">
          {emails.map((m) => (
            <button key={m.id} className="list-row clickable" style={{ width: "100%", border: 0, background: "none", textAlign: "left", borderBottom: "1px solid var(--line-2)" }} onClick={() => setOpen(m)}>
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ fontWeight: 560 }}>{m.subject}</div>
                <div className="cell-sub" style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>Alıcı: {m.recipient.fullName} · {m.body}</div>
              </div>
              <span className="small muted nowrap">{relativeTime(m.createdAt)}</span>
            </button>
          ))}
        </div>
      )}
      <Modal open={!!open} onClose={() => setOpen(null)} title={open?.subject ?? ""} desc={open ? `Alıcı: ${open.recipient.fullName} · ${formatDateTime(open.createdAt, timezone)}` : undefined} footer={<Button onClick={() => setOpen(null)}>Kapat</Button>}>
        <p style={{ whiteSpace: "pre-wrap" }}>{open?.body}</p>
      </Modal>
    </div>
  );
}
