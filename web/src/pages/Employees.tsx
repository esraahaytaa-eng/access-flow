import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useNavigate } from "react-router";
import { api, ApiError } from "../api/client";
import { useCompanyCatalog, useEmployees, useStarterPackages } from "../api/queries";
import type { CatalogEntry, Employee, EmployeeResponse, EmployeeStatus } from "../api/types";
import { errorMessage } from "../i18n/errors";
import { roleLabel } from "../i18n/labels";
import { useSession } from "../session";
import {
  Button, Callout, EmptyState, Field, Input, Modal, PageHeader, Person, SearchBox, Segmented, Select, StatusBadge, TableSkeleton, Tag, ToolCell, useAction,
} from "../ui/components";
import { formatDate } from "../ui/format";
import { statusBadge } from "../ui/status";

const lower = (s: string) => s.toLocaleLowerCase("tr-TR");

/** The departments employees hold (for the directory filter, retired ones included). */
export const departmentsOf = (employees: Employee[]) => [...new Set(employees.map((e) => e.department).filter((d): d is string => !!d))].sort((a, b) => a.localeCompare(b, "tr"));

export const managerOptions = (employees: Employee[], except?: string) =>
  employees.filter((e) => e.status === "ACTIVE" && e.id !== except).sort((a, b) => a.fullName.localeCompare(b.fullName, "tr"))
    .map((e) => ({ value: e.id, label: e.jobTitle ? `${e.fullName} — ${e.jobTitle}` : e.fullName }));

// catalogOptions are the active catalog entries; a retired current value stays selectable
// so an edit never drops it silently (holders keep retired entries, F3).
export function catalogOptions(entries: CatalogEntry[] | undefined, current?: string | null) {
  const options = (entries ?? []).filter((e) => e.active).map((e) => ({ value: e.name, label: e.name }));
  if (current && !options.some((o) => o.value === current)) options.unshift({ value: current, label: `${current} (kullanımdan kaldırıldı)` });
  return options;
}

export function EmployeesPage() {
  const navigate = useNavigate();
  const { timezone } = useSession();
  const query = useEmployees();
  const employees = (query.data ?? []) as Employee[];
  const [q, setQ] = useState("");
  const [status, setStatus] = useState<EmployeeStatus | "ALL">("ACTIVE");
  const [dept, setDept] = useState("");
  const [createOpen, setCreateOpen] = useState(false);
  const [catalogOpen, setCatalogOpen] = useState(false);
  const count = (s: EmployeeStatus) => employees.filter((e) => e.status === s).length;
  const nameOf = (id: string | null) => employees.find((e) => e.id === id)?.fullName;
  const rows = employees
    .filter((e) => (status === "ALL" || e.status === status) && (!dept || e.department === dept))
    .filter((e) => !q.trim() || [e.fullName, e.email, e.jobTitle ?? ""].some((f) => lower(f).includes(lower(q.trim()))));

  return (
    <>
      <PageHeader title="Çalışanlar" sub="İşe girişler, ekip değişiklikleri, askıya alma ve işten çıkışlar. Her değişiklik erişimlere otomatik yansır ve kayda geçer."
        actions={<><Button onClick={() => setCatalogOpen(true)}>Departmanlar ve unvanlar</Button><Button variant="primary" icon="plus" onClick={() => setCreateOpen(true)}>Yeni çalışan</Button></>} />
      {catalogOpen && <CatalogModal onClose={() => setCatalogOpen(false)} />}
      <div className="toolbar">
        <SearchBox value={q} onChange={setQ} placeholder="Ad, e-posta veya unvan ara" />
        <Segmented<EmployeeStatus | "ALL"> label="Durum" value={status} onChange={setStatus}
          options={[{ value: "ACTIVE", label: `Aktif (${count("ACTIVE")})` }, { value: "SUSPENDED", label: `Askıda (${count("SUSPENDED")})` }, { value: "TERMINATED", label: `Ayrılan (${count("TERMINATED")})` }, { value: "ALL", label: "Tümü" }]} />
        <div style={{ width: 200 }}><Select aria-label="Departman" value={dept} onChange={(e) => setDept(e.target.value)} placeholder="Tüm departmanlar" options={departmentsOf(employees).map((d) => ({ value: d, label: d }))} /></div>
      </div>
      {query.isPending ? <TableSkeleton /> : rows.length === 0 ? (
        <div className="card"><EmptyState icon="users" title="Eşleşen çalışan yok" desc="Filtreleri değiştirmeyi deneyin." /></div>
      ) : (
        <div className="card table-wrap">
          <table className="table">
            <thead><tr><th>Çalışan</th><th>Departman</th><th>Yönetici</th><th>Durum</th><th>Roller</th></tr></thead>
            <tbody>
              {rows.map((e) => (
                <tr key={e.id} className="clickable" tabIndex={0} onClick={() => navigate(`/employees/${e.id}`)} onKeyDown={(k) => k.key === "Enter" && navigate(`/employees/${e.id}`)}>
                  <td><Person person={e} sub={e.email} /></td>
                  <td><div>{e.department ?? "—"}</div><div className="cell-sub">{e.jobTitle}</div></td>
                  <td className="small">{nameOf(e.managerId) ?? <span className="muted">Yönetim Kurulu</span>}</td>
                  <td>
                    <div className="stack-sm" style={{ gap: 2 }}>
                      <StatusBadge badge={statusBadge("employee", e.status)} />
                      {e.termination && <span className="cell-sub" style={{ color: "var(--amber)" }}>Planlı çıkış: {formatDate(e.termination.at, timezone)}</span>}
                    </div>
                  </td>
                  <td><div className="row-wrap" style={{ gap: 4 }}>{e.roles.slice(0, 2).map((r) => <Tag key={r}>{roleLabel[r]}</Tag>)}</div></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {createOpen && <CreateEmployeeModal employees={employees} onClose={() => setCreateOpen(false)} />}
    </>
  );
}

type Form = { fullName: string; email: string; jobTitle: string; department: string; managerId: string };

function CreateEmployeeModal({ employees, onClose }: { employees: Employee[]; onClose: () => void }) {
  const qc = useQueryClient();
  const catalog = useCompanyCatalog().data;
  const [form, setForm] = useState<Form>({ fullName: "", email: "", jobTitle: "", department: "", managerId: "" });
  const [errors, setErrors] = useState<Partial<Record<keyof Form, string>>>({});
  const [created, setCreated] = useState<EmployeeResponse | null>(null);
  const [run, busy] = useAction();
  const set = (k: keyof Form) => (e: { target: { value: string } }) => { setForm({ ...form, [k]: e.target.value }); setErrors({ ...errors, [k]: undefined }); };

  const submit = async () => {
    const next: typeof errors = {
      fullName: form.fullName.trim() ? undefined : "Ad soyad girin.",
      email: !/^\S+@\S+\.\S+$/.test(form.email.trim()) ? "Geçerli bir e-posta adresi girin." : undefined,
      jobTitle: form.jobTitle.trim() ? undefined : "Unvan girin.",
      department: form.department.trim() ? undefined : "Departman girin.",
    };
    setErrors(next);
    if (Object.values(next).some(Boolean)) return;
    const r = await run(() => api.post<EmployeeResponse>("/employees", {
      email: form.email.trim(), fullName: form.fullName.trim(), department: form.department.trim(), jobTitle: form.jobTitle.trim(), managerId: form.managerId || null,
    }), { success: "Çalışan oluşturuldu" });
    if (r.ok) { setCreated(r.res); qc.invalidateQueries({ queryKey: ["employees"] }); }
    else if (r.err instanceof ApiError && r.err.field && r.err.field in form) setErrors({ [r.err.field]: errorMessage(r.err) });
  };

  if (created) return <CreatedResult title={`${created.employee.fullName} oluşturuldu`} employee={created.employee} onClose={onClose} />;
  return (
    <Modal open wide onClose={onClose} title="Yeni çalışan" desc="İşe giriş kaydı oluşturun. Çalışan parolasını etkinleştirme bağlantısıyla kendisi belirler."
      footer={<><Button onClick={onClose}>Vazgeç</Button><Button variant="primary" loading={busy} onClick={submit}>Çalışanı oluştur</Button></>}>
      <div className="form-grid">
        <Field label="Ad soyad" required error={errors.fullName}><Input value={form.fullName} invalid={!!errors.fullName} onChange={set("fullName")} placeholder="Örn. Kaan Uslu" /></Field>
        <Field label="E-posta" required error={errors.email}><Input type="email" value={form.email} invalid={!!errors.email} onChange={set("email")} placeholder="kaan.uslu@accessflow.com.tr" /></Field>
        <Field label="Unvan" required error={errors.jobTitle}><Select value={form.jobTitle} invalid={!!errors.jobTitle} onChange={set("jobTitle")} placeholder="Unvan seçin" options={catalogOptions(catalog?.jobPositions)} /></Field>
        <Field label="Departman" required error={errors.department}><Select value={form.department} invalid={!!errors.department} onChange={set("department")} placeholder="Departman seçin" options={catalogOptions(catalog?.departments)} /></Field>
        <Field label="Yönetici" error={errors.managerId} hint="Yönetici, çalışanın taleplerinde ilk onaylayıcıdır.">
          <Select value={form.managerId} onChange={set("managerId")} placeholder="Yönetici yok (en üst düzey)" options={managerOptions(employees)} />
        </Field>
      </div>
    </Modal>
  );
}

// CatalogModal is where HR defines the company's departments and job titles (M17): exact
// names, added and retired, never deleted. They grant nothing.
function CatalogModal({ onClose }: { onClose: () => void }) {
  const catalog = useCompanyCatalog();
  return (
    <Modal open wide onClose={onClose} title="Departmanlar ve unvanlar" desc="Şirketin kullandığı adlar, yazıldığı gibi saklanır. Yetki vermezler; başlangıç paketlerini belirler. Kaldırılan bir ad, onu taşıyan çalışanlarda kalır ama yeni atamalarda kullanılamaz."
      footer={<Button onClick={onClose}>Kapat</Button>}>
      {catalog.isPending ? <TableSkeleton rows={3} /> : (
        <div className="form-grid">
          <CatalogList title="Departmanlar" list="departments" entries={catalog.data?.departments ?? []} />
          <CatalogList title="Unvanlar" list="job-positions" entries={catalog.data?.jobPositions ?? []} />
        </div>
      )}
    </Modal>
  );
}

function CatalogList({ title, list, entries }: { title: string; list: "departments" | "job-positions"; entries: CatalogEntry[] }) {
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const [run, busy] = useAction();
  const refresh = () => qc.invalidateQueries({ queryKey: ["company-catalog"] });
  return (
    <div className="stack-sm">
      <div className="section-title">{title}</div>
      <ul className="stack-sm" style={{ listStyle: "none", margin: 0, padding: 0 }}>
        {entries.map((e) => (
          <li key={e.id} className="row" style={{ justifyContent: "space-between" }}>
            <span className={e.active ? undefined : "muted"}>{e.name}{!e.active && " (kullanımdan kaldırıldı)"}</span>
            {e.active && <Button size="sm" variant="danger-ghost" aria-label={`${e.name} kaldır`} loading={busy}
              onClick={() => run(() => api.post(`/company-catalog/${list}/${e.id}/retire`), { success: `${e.name} kullanımdan kaldırıldı` }).then((r) => { if (r.ok) refresh(); })}>Kaldır</Button>}
          </li>
        ))}
      </ul>
      <div className="row">
        <Input aria-label={`Yeni ${title.toLowerCase()} adı`} value={name} onChange={(e) => setName(e.target.value)} placeholder="Örn. Engineering" />
        <Button disabled={!name.trim()} loading={busy}
          onClick={() => run(() => api.post(`/company-catalog/${list}`, { name }), { success: `${name.trim()} eklendi` }).then((r) => { if (r.ok) { setName(""); refresh(); } })}>Ekle</Button>
      </div>
    </div>
  );
}

/** After a create or rehire: the activation link went to the Mock Email Outbox, never to this screen (M18). */
export function CreatedResult({ title, employee, onClose }: { title: string; employee: Employee; onClose: () => void }) {
  const navigate = useNavigate();
  const packages = (useStarterPackages().data ?? []).filter((p) => p.requestable && p.department === employee.department && (!p.jobTitle || p.jobTitle === employee.jobTitle));
  return (
    <Modal open onClose={onClose} title={title}
      footer={<><Button onClick={onClose}>Kapat</Button>{packages.length > 0 && <Button variant="primary" icon="send" onClick={() => navigate(`/request-new?for=${employee.id}&pkg=1`)}>Başlangıç paketini talep et</Button>}</>}>
      <div className="stack">
        <Callout tone="info">Etkinleştirme bağlantısı Mock Email Outbox'a gönderildi. Bağlantı 24 saat geçerlidir ve tek kullanımlıktır; yalnızca Süper Yönetici e-posta kutusunda görüntüleyebilir.</Callout>
        {packages.length > 0 && (
          <div>
            <div className="section-title">Önerilen başlangıç paketi · {employee.department}</div>
            <div className="stack-sm">{packages.map((p) => <ToolCell key={p.id} tool={p.tool} level={p.level} />)}</div>
            <p className="hint" style={{ marginTop: 8 }}>Öneri bağlayıcı değildir. Talep gönderildiğinde yönetici ve gerekli diğer onaylayıcılar normal şekilde karar verir.</p>
          </div>
        )}
      </div>
    </Modal>
  );
}
