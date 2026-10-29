import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useNavigate } from "react-router";
import { api, ApiError } from "../api/client";
import { useEmployees, useStarterPackages } from "../api/queries";
import type { Employee, EmployeeStatus, EmployeeWithPassword } from "../api/types";
import { errorMessage } from "../i18n/errors";
import { roleLabel } from "../i18n/labels";
import { useSession } from "../session";
import {
  Button, Callout, EmptyState, Field, Input, Modal, PageHeader, Person, SearchBox, Segmented, Select, StatusBadge, TableSkeleton, Tag, ToolCell, useAction, useToast,
} from "../ui/components";
import { formatDate } from "../ui/format";
import { statusBadge } from "../ui/status";

const lower = (s: string) => s.toLocaleLowerCase("tr-TR");

/** Departments are free text in the API; offer the ones already in use. */
export const departmentsOf = (employees: Employee[]) => [...new Set(employees.map((e) => e.department).filter((d): d is string => !!d))].sort((a, b) => a.localeCompare(b, "tr"));

export const managerOptions = (employees: Employee[], except?: string) =>
  employees.filter((e) => e.status === "ACTIVE" && e.id !== except).sort((a, b) => a.fullName.localeCompare(b.fullName, "tr"))
    .map((e) => ({ value: e.id, label: e.jobTitle ? `${e.fullName} — ${e.jobTitle}` : e.fullName }));

export function EmployeesPage() {
  const navigate = useNavigate();
  const { timezone } = useSession();
  const query = useEmployees();
  const employees = (query.data ?? []) as Employee[];
  const [q, setQ] = useState("");
  const [status, setStatus] = useState<EmployeeStatus | "ALL">("ACTIVE");
  const [dept, setDept] = useState("");
  const [createOpen, setCreateOpen] = useState(false);
  const count = (s: EmployeeStatus) => employees.filter((e) => e.status === s).length;
  const nameOf = (id: string | null) => employees.find((e) => e.id === id)?.fullName;
  const rows = employees
    .filter((e) => (status === "ALL" || e.status === status) && (!dept || e.department === dept))
    .filter((e) => !q.trim() || [e.fullName, e.email, e.jobTitle ?? ""].some((f) => lower(f).includes(lower(q.trim()))));

  return (
    <>
      <PageHeader title="Çalışanlar" sub="İşe girişler, ekip değişiklikleri, askıya alma ve işten çıkışlar. Her değişiklik erişimlere otomatik yansır ve kayda geçer."
        actions={<Button variant="primary" icon="plus" onClick={() => setCreateOpen(true)}>Yeni çalışan</Button>} />
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
  const [form, setForm] = useState<Form>({ fullName: "", email: "", jobTitle: "", department: "", managerId: "" });
  const [errors, setErrors] = useState<Partial<Record<keyof Form, string>>>({});
  const [created, setCreated] = useState<EmployeeWithPassword | null>(null);
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
    const r = await run(() => api.post<EmployeeWithPassword>("/employees", {
      email: form.email.trim(), fullName: form.fullName.trim(), department: form.department.trim(), jobTitle: form.jobTitle.trim(), managerId: form.managerId || null,
    }), { success: "Çalışan oluşturuldu" });
    if (r.ok) { setCreated(r.res); qc.invalidateQueries({ queryKey: ["employees"] }); }
    else if (r.err instanceof ApiError && r.err.field && r.err.field in form) setErrors({ [r.err.field]: errorMessage(r.err) });
  };

  if (created) return <PasswordResult title={`${created.employee.fullName} oluşturuldu`} employee={created.employee} password={created.temporaryPassword} onClose={onClose} />;
  return (
    <Modal open wide onClose={onClose} title="Yeni çalışan" desc="İşe giriş kaydı oluşturun. Geçici parola otomatik üretilir."
      footer={<><Button onClick={onClose}>Vazgeç</Button><Button variant="primary" loading={busy} onClick={submit}>Çalışanı oluştur</Button></>}>
      <div className="form-grid">
        <Field label="Ad soyad" required error={errors.fullName}><Input value={form.fullName} invalid={!!errors.fullName} onChange={set("fullName")} placeholder="Örn. Kaan Uslu" /></Field>
        <Field label="E-posta" required error={errors.email}><Input type="email" value={form.email} invalid={!!errors.email} onChange={set("email")} placeholder="kaan.uslu@accessflow.com.tr" /></Field>
        <Field label="Unvan" required error={errors.jobTitle}><Input value={form.jobTitle} invalid={!!errors.jobTitle} onChange={set("jobTitle")} placeholder="Örn. Yazılım Mühendisi" /></Field>
        <Field label="Departman" required error={errors.department}><Input list="departments" value={form.department} invalid={!!errors.department} onChange={set("department")} /></Field>
        <Field label="Yönetici" error={errors.managerId} hint="Yönetici, çalışanın taleplerinde ilk onaylayıcıdır.">
          <Select value={form.managerId} onChange={set("managerId")} placeholder="Yönetici yok (en üst düzey)" options={managerOptions(employees)} />
        </Field>
      </div>
      <datalist id="departments">{departmentsOf(employees).map((d) => <option key={d} value={d} />)}</datalist>
    </Modal>
  );
}

/** A temporary password is shown exactly once (the response is not cached). */
export function PasswordResult({ title, employee, password, onClose }: { title: string; employee: Employee; password: string; onClose: () => void }) {
  const navigate = useNavigate();
  const toast = useToast();
  const packages = (useStarterPackages().data ?? []).filter((p) => p.requestable && p.department === employee.department && (!p.jobTitle || p.jobTitle === employee.jobTitle));
  const copy = async () => {
    try { await navigator.clipboard.writeText(password); toast({ type: "info", title: "Parola kopyalandı" }); } catch { /* clipboard blocked: the password stays visible */ }
  };
  return (
    <Modal open onClose={onClose} title={title} desc="Geçici parola yalnızca bir kez gösterilir."
      footer={<><Button onClick={onClose}>Kapat</Button>{packages.length > 0 && <Button variant="primary" icon="send" onClick={() => navigate(`/request-new?for=${employee.id}&pkg=1`)}>Başlangıç paketini talep et</Button>}</>}>
      <div className="stack">
        <div className="secret" aria-label="Geçici parola">{password}</div>
        <div className="row" style={{ justifyContent: "center" }}><Button size="sm" icon="copy" onClick={copy}>Kopyala</Button></div>
        <Callout tone="warn">Parola 72 saat geçerlidir ve tek kullanımlıktır. İlk girişte çalışan yeni parola belirlemek zorundadır. Bu pencereyi kapattıktan sonra parola tekrar görüntülenemez.</Callout>
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
