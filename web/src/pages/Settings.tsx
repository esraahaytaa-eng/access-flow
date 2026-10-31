import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api, ApiError, saveFile } from "../api/client";
import { useAdminGroups, useAdminHealth, useCompanyCatalog, useEmployees, useNominations, useReviews, useStarterPackages, useTools } from "../api/queries";
import type { Activation, ActivationsResponse, ActivationStatus, AdminGroup, AssignRoleBody, Employee, ImportReport, ImportsResponse, Role, StarterPackage, WorkbookPlan, WorkbookProblem, WorkbookResult } from "../api/types";
import { roleLabel } from "../i18n/labels";
import { useSession } from "../session";
import {
  Badge, Button, Callout, Card, ConfirmDialog, EmptyState, Field, FilePicker, Input, KeyValueList, Modal, PageHeader, Person, Segmented, Select, TableSkeleton, Tabs, Textarea, useAction,
} from "../ui/components";
import { formatDate, formatDateTime } from "../ui/format";
import { reissueWarning } from "./EmployeeDetail";
import { catalogOptions, managerOptions } from "./Employees";
import { Mailbox } from "./Mailbox";
import { NominateModal } from "./Succession";

type Tab = "roles" | "groups" | "packages" | "reviews" | "imports" | "activations" | "mail" | "system";
const assignable: AssignRoleBody["role"][] = ["INFOSEC", "HR_MANAGER", "AUDITOR"];

export function SettingsPage() {
  const [tab, setTab] = useState<Tab>("roles");
  return (
    <>
      <PageHeader title="Yönetim ayarları" sub="Ayrıcalıklı roller, yönetim grupları, başlangıç paketleri, denetim takvimi, içe aktarma ve e-posta kutusu." />
      <Tabs value={tab} onChange={setTab} tabs={[{ value: "roles", label: "Roller" }, { value: "groups", label: "Yönetim grupları" }, { value: "packages", label: "Başlangıç paketleri" },
        { value: "reviews", label: "Denetim takvimi" }, { value: "imports", label: "İçe aktarma" },
        { value: "activations", label: "Hesap etkinleştirme" }, { value: "mail", label: "E-posta kutusu" }, { value: "system", label: "Sistem" }]} />
      {tab === "roles" ? <RolesTab /> : tab === "groups" ? <GroupsTab /> : tab === "packages" ? <PackagesTab /> : tab === "reviews" ? <ReviewScheduleTab />
        : tab === "imports" ? <ImportsTab /> : tab === "activations" ? <ActivationsTab /> : tab === "mail" ? <Mailbox /> : <SystemTab />}
    </>
  );
}

function RolesTab() {
  const qc = useQueryClient();
  const { me, timezone } = useSession();
  const query = useEmployees();
  const employees = (query.data ?? []) as Employee[];
  const [assignOpen, setAssignOpen] = useState(false);
  const [nominateOpen, setNominateOpen] = useState(false);
  const nominations = useNominations().data ?? [];
  const [remove, setRemove] = useState<{ e: Employee; r: Role } | null>(null);
  const order: Role[] = ["SUPER_ADMIN", "INFOSEC", "HR_MANAGER", "AUDITOR"];
  const rows = order.flatMap((r) => employees.filter((e) => e.status !== "TERMINATED" && e.roles.includes(r)).map((e) => ({ e, r })));
  return (
    <div className="stack">
      <Card title="Ayrıcalıklı roller" sub="Kimse kendi rollerini değiştiremez. Rol kaldırıldığında geçmiş kaydı korunur." pad={false}
        actions={<><Button size="sm" onClick={() => setNominateOpen(true)}>Süper Yönetici öner</Button><Button size="sm" variant="primary" icon="plus" onClick={() => setAssignOpen(true)}>Rol ata</Button></>}>
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
      {nominations.length > 0 && (
        <Card title="Bekleyen Süper Yönetici önerileri" pad={false}>
          {nominations.map((n) => (
            <div key={n.id} className="list-row">
              <div style={{ flex: 1 }}><Person person={n.nominee} sub={`Öneren: ${n.proposedBy.fullName} · ${formatDateTime(n.createdAt, timezone)} · ${formatDate(n.expiresAt, timezone)} tarihinde sona erer`} /></div>
              <Badge tone="amber" dot>Yetkilendirme bekliyor</Badge>
            </div>
          ))}
        </Card>
      )}
      {assignOpen && <AssignRoleModal employees={employees} onClose={() => setAssignOpen(false)} />}
      {nominateOpen && <NominateModal employees={employees} onClose={() => setNominateOpen(false)} />}
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
    <Modal open onClose={onClose} title="Rol ata" desc="Süper Yönetici rolü bu ekrandan atanamaz; iki kişilik öneri süreci kullanılır."
      footer={<><Button onClick={onClose}>Vazgeç</Button><Button variant="primary" loading={busy} onClick={submit}>Ata</Button></>}>
      <div className="stack">
        <Field label="Kişi" required error={errors.emp}><Select value={employeeId} onChange={(e) => { setEmployeeId(e.target.value); setErrors({ ...errors, emp: undefined }); }} placeholder="Seçin" options={managerOptions(employees, me.id)} /></Field>
        <Field label="Rol" required error={errors.role}><Select value={role} onChange={(e) => { setRole(e.target.value); setErrors({ ...errors, role: undefined }); }} placeholder="Seçin" options={assignable.map((r) => ({ value: r, label: roleLabel[r] }))} /></Field>
        {role === "AUDITOR" && <Callout tone="info">Denetçi rolü dış denetçi içindir: hesap yalnızca denetim kayıtlarını görüntüler, filtreler ve CSV olarak indirir. Rol, hesabın diğer rollerinin ve yetkilerinin önüne geçer.</Callout>}
      </div>
    </Modal>
  );
}

function SystemTab() {
  const { timezone } = useSession();
  const health = useAdminHealth();
  const employees = (useEmployees().data ?? []) as Employee[];
  const tools = useTools().data ?? [];
  return (
    <div className="grid-2">
      <Card title="Sistem">
        <KeyValueList items={[
          ["Şirket saat dilimi", timezone],
          ["Aktif Süper Yönetici", health.data ? `${health.data.activeSuperAdmins}` : "—"],
          ["Bilgi Güvenliği üyesi", health.data ? `${health.data.infosecMembers}` : "—"],
          ["Çalışan", `${employees.filter((e) => e.status === "ACTIVE").length} aktif · ${employees.length} toplam`],
          ["Araç", `${tools.filter((t) => t.status === "ACTIVE").length} kullanımda`],
        ]} />
      </Card>
      <Card title="Uygulanan temel kurallar">
        <ul className="small" style={{ margin: 0, paddingLeft: 18, display: "grid", gap: 6, color: "var(--ink-2)" }}>
          <li>Onay gereksinimleri erişim seviyesine aittir; yönetici onayı her zaman gerekir.</li>
          <li>Adımlar sıralıdır ve her adıma farklı bir kişi karar verir; hak sahibi hiçbir adıma karar veremez.</li>
          <li>Hiçbir onay otomatik verilmez; API sağlama yalnızca tüm onaylardan sonra yapılır.</li>
          <li>Erişim, API’de doğrulandığında veya manuel/tedarikçi işlemde yetkili kişi onayladığında aktif sayılır.</li>
          <li>Denetim kaydı yalnızca eklemeye açıktır; geçmiş kayıtlar değiştirilmez.</li>
        </ul>
      </Card>
    </div>
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
  const catalog = useCompanyCatalog().data;
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
          <Field label="Departman" required><Select value={form.department} onChange={(e) => setForm({ ...form, department: e.target.value })} placeholder="Departman seçin" options={catalogOptions(catalog?.departments)} /></Field>
          <Field label="Unvan" hint="Boş bırakılırsa tüm departmana uygulanır."><Select value={form.jobTitle} onChange={(e) => setForm({ ...form, jobTitle: e.target.value })} placeholder="Tüm departman" options={catalogOptions(catalog?.jobPositions)} /></Field>
          <Field label="Araç ve seviye" required className="full"><Select value={form.levelId} onChange={(e) => setForm({ ...form, levelId: e.target.value })} placeholder="Seçin" options={levelOptions} /></Field>
        </div>
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

const activationLabel: Record<ActivationStatus, string> = { PENDING: "Bekliyor", EXPIRED: "Süresi doldu", COMPLETED: "Tamamlandı", NO_LINK: "Bağlantı yok" };
const activationTone = { PENDING: "amber", EXPIRED: "red", COMPLETED: "green", NO_LINK: "outline" } as const;
const reasonLabel = { CREATED: "Yeni çalışan", IMPORTED: "Excel içe aktarma", REHIRED: "Yeniden işe alım", RESET: "Yenilendi" };

// Not in the design: the Super Admin's view of first sign-ins (M18). Employees with a password and no link are not listed.
function ActivationsTab() {
  const { timezone } = useSession();
  const qc = useQueryClient();
  const query = useQuery({ queryKey: ["activations"], queryFn: () => api.get<ActivationsResponse>("/activations").then((r) => r.activations) });
  const [filter, setFilter] = useState<ActivationStatus | "ALL">("PENDING");
  const [reissue, setReissue] = useState<Activation | null>(null);
  const all = query.data ?? [];
  const rows = filter === "ALL" ? all : all.filter((a) => a.status === filter);
  const count = (s: ActivationStatus) => all.filter((a) => a.status === s).length;
  return (
    <div className="stack">
      <Callout tone="info">Çalışanlar parolalarını tek kullanımlık, 24 saat geçerli bir bağlantıyla kendileri belirler. Kimse bir çalışanın parolasını göremez. Yeni bağlantı öncekileri geçersiz kılar.</Callout>
      <Segmented label="Durum" value={filter} onChange={setFilter} options={[
        { value: "PENDING", label: `Bekleyen (${count("PENDING")})` }, { value: "EXPIRED", label: `Süresi dolan (${count("EXPIRED")})` },
        { value: "NO_LINK", label: `Bağlantı yok (${count("NO_LINK")})` }, { value: "COMPLETED", label: `Tamamlanan (${count("COMPLETED")})` }, { value: "ALL", label: `Tümü (${all.length})` }]} />
      <Card pad={false}>
        {query.isPending ? <TableSkeleton /> : rows.length === 0 ? <EmptyState icon="check" title="Bu durumda çalışan yok" /> : (
          <div className="table-wrap">
            <table className="table">
              <thead><tr><th>Çalışan</th><th>Durum</th><th>Neden</th><th>Son geçerlilik</th><th>Tamamlanma</th><th /></tr></thead>
              <tbody>
                {rows.map((a) => (
                  <tr key={a.employee.id}>
                    <td><Person person={a.employee} sub={a.employee.email} /></td>
                    <td><Badge tone={activationTone[a.status]} dot>{activationLabel[a.status]}</Badge></td>
                    <td className="small">{a.reason ? reasonLabel[a.reason] : "—"}</td>
                    <td className="small nowrap">{a.expiresAt && a.status !== "COMPLETED" ? formatDateTime(a.expiresAt, timezone) : "—"}</td>
                    <td className="small nowrap">{a.completedAt ? formatDateTime(a.completedAt, timezone) : "—"}</td>
                    <td className="actions"><Button size="sm" onClick={() => setReissue(a)}>Yeni bağlantı gönder</Button></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
      <ConfirmDialog open={!!reissue} onClose={() => setReissue(null)} title={`${reissue?.employee.fullName ?? ""} için yeni bağlantı gönderilsin mi?`}
        desc={reissueWarning(reissue?.status === "COMPLETED")} confirmLabel="Bağlantı gönder" success="Bağlantı gönderildi" successDesc="Etkinleştirme bağlantısı Mock Email Outbox'a gönderildi."
        onConfirm={async () => { await api.post(`/employees/${reissue!.employee.id}/password-reset`); qc.invalidateQueries({ queryKey: ["activations"] }); qc.invalidateQueries({ queryKey: ["email-outbox"] }); }} />
    </div>
  );
}

function ReviewScheduleTab() {
  const { timezone } = useSession();
  const qc = useQueryClient();
  const reviews = useReviews();
  const [confirm, setConfirm] = useState(false);
  const campaign = reviews.data?.campaign;
  const waiting = reviews.data?.counts.waiting ?? 0;
  return (
    <div className="stack">
      <Card title="Üç aylık erişim denetimi takvimi">
        <KeyValueList items={[
          ["Sıklık", "Her takvim çeyreğinin başında otomatik"],
          ["Kapsam", "Tüm aktif ve askıdaki erişimler (süreli ve kaldırılana kadar)"],
          ["Gün 7", "Yanıtlanmamış her kalem için incelemeciye tek hatırlatma"],
          ["Gün 14", "Yanıtlanmamış kalemler Bilgi Güvenliğine eskale edilir"],
          ["Gün 21", "Yanıtlanmamış erişimler için güvenlik bulgusu oluşturulur ve Bilgi Güvenliği tarafından takip edilir. Otomatik erişim kaldırılmaz."],
        ]} />
      </Card>
      <Card title="Ek erişim denetimi" sub="Gerektiğinde (örneğin büyük bir içe aktarmadan sonra) erişim denetimini elle başlatabilirsiniz.">
        {reviews.isPending ? <TableSkeleton rows={1} /> : campaign ? (
          <Callout tone="info" title="Devam eden erişim denetimi var">{formatDate(campaign.startedAt, timezone)} tarihinde başladı · {waiting} kalem yanıt bekliyor. Devam eden denetim bitmeden yenisi başlatılamaz.</Callout>
        ) : <Button variant="primary" onClick={() => setConfirm(true)}>Erişim denetimi başlat</Button>}
      </Card>
      <ConfirmDialog open={confirm} onClose={() => setConfirm(false)} title="Yeni erişim denetimi başlatılsın mı?" confirmLabel="Başlat" success="Erişim denetimi başlatıldı"
        desc="Tüm aktif erişimler anlık olarak denetime alınır ve araç sahiplerine bildirim gönderilir. Açık bulgusu olan kalemler eklenmez."
        onConfirm={async () => { await api.post("/reviews/campaigns", {}); qc.invalidateQueries({ queryKey: ["reviews"] }); }} />
    </div>
  );
}

const rejectionText = { unknown_tool: "bilinmeyen araç", unknown_level: "bilinmeyen seviye", api_tool: "API aracı; Excel ile veya araç servisinden aktarın", invalid_row: "geçersiz satır" };
const csvExample = "eposta;arac;seviye;hesap\nayse.demir@sirket.com;Figma;Editör;\n";

const workbookReasons: Record<string, string> = {
  invalid_workbook: "Dosya okunamadı; şifresiz bir .xlsx dosyası yükleyin",
  macros_not_allowed: "Makro içeren dosyalar kabul edilmez",
  formula_not_allowed: "Formül kabul edilmez; değeri yazı olarak girin",
  missing_sheet: "Sayfa eksik",
  invalid_header: "Sütun başlığı şablondakinden farklı",
  too_many_rows: "Satır sınırı aşıldı",
  too_many_columns: "Z sütunundan sonra veri olamaz",
  empty_workbook: "Dosyada aktarılacak satır yok",
  sample_row: "Örnek satır; silin veya gerçek veriyle değiştirin",
  field_required: "Zorunlu alan boş",
  invalid_email: "Geçersiz e-posta adresi",
  invalid_text: "Geçersiz metin",
  duplicate_employee: "Aynı çalışan dosyada tekrar ediyor",
  conflicting_employee: "Aynı e-posta farklı bilgilerle tekrar ediyor",
  unknown_department: "Tanımlı olmayan departman",
  retired_department: "Kullanımdan kaldırılmış departman",
  unknown_job_position: "Tanımlı olmayan unvan",
  retired_job_position: "Kullanımdan kaldırılmış unvan",
  unknown_manager: "Yönetici bulunamadı",
  inactive_manager: "Yönetici aktif değil",
  self_manager: "Çalışan kendi yöneticisi olamaz",
  manager_cycle: "Yönetici zinciri döngü oluşturuyor",
  invalid_fulfillment_method: "Geçersiz karşılama yöntemi",
  invalid_approval_steps: "Geçersiz onay adımları",
  unknown_owner: "Araç sahibi bulunamadı",
  inactive_owner: "Araç sahibi aktif değil",
  unknown_admin_group: "Tanımlı olmayan yönetim grubu",
  conflicting_tool: "Aynı aracın satırları farklı bilgiler içeriyor",
  duplicate_level: "Aynı seviye tekrar ediyor",
  retired_tool: "Kullanımdan kaldırılmış araç",
  retired_level: "Kullanımdan kaldırılmış seviye",
  unknown_employee: "Çalışan bulunamadı; Employees sayfasına ekleyin",
  unknown_tool: "Araç bulunamadı; Tools sayfasına ekleyin",
  unknown_level: "Seviye bulunamadı; Tools sayfasına ekleyin",
  api_account_mismatch: "API araçlarında hesap, çalışanın e-postası olmalı",
  duplicate_access: "Aynı erişim tekrar ediyor",
  conflicting_access: "Aynı hesap birden fazla çalışana yazılmış",
  employee_differs: "Kayıtlı bilgiden farklı; mevcut kayıt değiştirilmez",
  employee_not_active: "Çalışan aktif değil; kayıt değiştirilmez",
  tool_differs: "Kayıtlı araç bilgisinden farklı; mevcut kayıt değiştirilmez",
  level_differs: "Kayıtlı onay adımlarından farklı; mevcut kayıt değiştirilmez",
  employee_terminated: "Ayrılmış çalışan; erişim aktarılır ve hemen kaldırma başlatılır",
  previously_removed: "Bu erişim daha önce aktarılıp kaldırılmış; yeniden eklenmez",
  held_by_other: "Bu hesap başka bir çalışana aktarılmış; atlanır",
};

function ProblemTable({ rows }: { rows: WorkbookProblem[] }) {
  return (
    <div className="table-wrap">
      <table className="table">
        <thead><tr><th>Sayfa</th><th>Satır</th><th>Sütun</th><th>Değer</th><th>Sorun</th><th>Beklenen</th></tr></thead>
        <tbody>
          {rows.map((p, k) => (
            <tr key={k}>
              <td>{p.sheet ?? "—"}</td><td>{p.row ?? "—"}</td><td>{p.column ?? "—"}</td><td>{p.value || "—"}</td>
              <td>{workbookReasons[p.reason] ?? p.reason}{p.otherRow ? ` (satır ${p.otherRow})` : ""}</td><td>{p.expected || "—"}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function WorkbookCard({ onImported }: { onImported: (r: ImportReport) => void }) {
  const [file, setFile] = useState<File | null>(null);
  const [plan, setPlan] = useState<WorkbookPlan | null>(null);
  const [run, busy] = useAction();
  const choose = (f: File | undefined) => { setFile(f ?? null); setPlan(null); };
  const preview = () => run(() => api.post<WorkbookPlan>("/imports/workbook/preview", file!)).then((r) => r.ok && setPlan(r.res));
  const submit = async () => {
    const r = await run(() => api.post<WorkbookResult>(`/imports/workbook?fileName=${encodeURIComponent(file!.name)}`, file!), { success: "Excel içe aktarıldı" });
    if (r.ok) { setPlan(null); setFile(null); onImported(r.res.report); return; }
    // The data changed since the preview: show the server's fresh findings.
    if (r.err instanceof ApiError && r.err.body?.plan) setPlan(r.err.body.plan as WorkbookPlan);
  };
  const download = () => run(() => api.download("/imports/workbook/template")).then((r) => r.ok && saveFile(r.res, `accessflow-onboarding-${new Date().toISOString().slice(0, 10)}.xlsx`));
  const s = plan?.summary;
  return (
    <Card title="Excel ile toplu aktarım" sub="İlk kurulum için ana yöntem: çalışanlar, araçlar ve mevcut erişimler tek dosyada aktarılır. Önce önizleyin; dosyada sorun varsa hiçbir şey kaydedilmez. Mevcut kayıtlar değiştirilmez."
      actions={<Button onClick={download}>Excel şablonunu indir</Button>}>
      <div className="stack">
        <div className="row-wrap">
          <FilePicker label="Excel dosyası" accept=".xlsx" fileName={file?.name} onFile={choose} />
          <Button disabled={!file} loading={busy} onClick={preview}>Önizle</Button>
          <Button variant="primary" disabled={!file || !plan || plan.problems.length > 0} loading={busy} onClick={submit}>Excel’i içe aktar</Button>
        </div>
        {s && (
          <div className="row-wrap" style={{ gap: 8 }}>
            <Badge tone="green">{s.employeesNew} yeni çalışan</Badge><Badge tone="outline">{s.employeesExisting} mevcut çalışan</Badge>
            <Badge tone="green">{s.toolsNew} yeni araç</Badge><Badge tone="green">{s.levelsNew} yeni seviye</Badge>
            <Badge tone="outline">{s.toolsExisting + s.levelsExisting} mevcut araç/seviye</Badge>
            <Badge tone="green">{s.accessNew} yeni erişim</Badge><Badge tone="outline">{s.accessExisting} zaten aktarılmış erişim</Badge>
          </div>
        )}
        {plan && plan.owners.length > 0 && (
          <div className="small"><div className="muted">Yeni araç sahipleri (Araç Sahibi rolü atanır):</div>{plan.owners.map((o) => <div key={o.tool}>{o.tool} → {o.ownerEmail}</div>)}</div>
        )}
        {plan && plan.problems.length > 0 && <><Callout tone="danger" title={`${plan.problems.length} sorun; düzeltip yeniden yükleyin`}>Dosyadaki hiçbir satır aktarılmaz.</Callout><ProblemTable rows={plan.problems} /></>}
        {plan && plan.warnings.length > 0 && <><Callout tone="warn" title={`${plan.warnings.length} uyarı`}>Uyarılar aktarımı engellemez.</Callout><ProblemTable rows={plan.warnings} /></>}
      </div>
    </Card>
  );
}

function ImportsTab() {
  const qc = useQueryClient();
  const imports = useQuery({ queryKey: ["imports"], queryFn: () => api.get<ImportsResponse>("/imports").then((r) => r.imports) });
  const [content, setContent] = useState("");
  const [fileName, setFileName] = useState("");
  const [last, setLast] = useState<ImportReport | null>(null);
  const [run, busy] = useAction();
  const done = (r: { ok: boolean; res?: ImportReport }) => {
    if (!r.ok || !r.res) return;
    setLast(r.res);
    qc.invalidateQueries();
  };
  const readFile = (file: File | undefined) => {
    if (!file) return;
    setFileName(file.name);
    file.text().then(setContent);
  };
  return (
    <div className="stack">
      <WorkbookCard onImported={(r) => done({ ok: true, res: r })} />
      <details className="advanced">
        <summary>Gelişmiş içe aktarma seçenekleri</summary>
        <p className="muted small">İlk kurulumdan sonra erişimleri gerçek durumla karşılaştırmak (mutabakat) içindir: API araçlarından okunan hesaplar Excel’de bildirilen erişimleri doğrular, hiçbir çalışana ait olmayan hesapları ise İstisnalar’a sahipsiz hesap olarak ekler. Aynı erişim ikinci kez aktarılmaz.</p>
        <div className="stack">
          <Card title="API araçlarının mevcut erişimleri" sub="Mock Araç Servisi’ndeki hesaplar okunur ve kurumsal e-posta ile çalışanlara eşleştirilir. Aynı erişim ikinci kez aktarılmaz.">
            <Button loading={busy} onClick={() => run(() => api.post<ImportReport>("/imports/mock", {}), { success: "İçe aktarma tamamlandı" }).then(done)}>Araç servisinden içe aktar</Button>
          </Card>
          <Card title="Manuel ve tedarikçi araçları için CSV içe aktarma" sub="Araçtan alınan hesap listesini aktarır; hiçbir çalışanla eşleşmeyen hesaplar sahipsiz hesap olur. Sütunlar: eposta; arac; seviye; hesap. Hesap boşsa e-posta adresi kullanılır.">
            <div className="stack">
              <FilePicker label="CSV dosyası" accept=".csv,text/csv" fileName={fileName} onFile={readFile} />
              <Field label="İçerik" hint="Bilinmeyen araç veya seviye içeren satırlar reddedilir ve raporda listelenir.">
                <Textarea rows={6} value={content} onChange={(e) => setContent(e.target.value)} placeholder={csvExample} style={{ fontFamily: "var(--mono)", fontSize: 12.5 }} />
              </Field>
              <div className="row" style={{ justifyContent: "flex-end" }}>
                <Button disabled={!content.trim()} loading={busy}
                  onClick={() => run(() => api.post<ImportReport>("/imports/csv", { fileName: fileName || "yapistirilan_veri.csv", content }), { success: "İçe aktarma tamamlandı" }).then(done)}>İçe aktar</Button>
              </div>
            </div>
          </Card>
        </div>
      </details>
      {last && <ImportReportCard report={last} highlight />}
      <div className="section-title" style={{ marginTop: 8 }}>Önceki içe aktarmalar</div>
      {imports.isPending ? <TableSkeleton rows={2} /> : (imports.data ?? []).filter((r) => r.id !== last?.id).map((r) => <ImportReportCard key={r.id} report={r} />)}
    </div>
  );
}

function ImportReportCard({ report: r, highlight }: { report: ImportReport; highlight?: boolean }) {
  const { timezone } = useSession();
  return (
    <Card title={r.source === "MOCK" ? "Araç servisi" : `${r.source === "WORKBOOK" ? "Excel" : "CSV"} · ${r.fileName}`} sub={`${formatDateTime(r.importedAt, timezone)} · ${r.importedBy.fullName}`} style={highlight ? { borderColor: "var(--accent-line)" } : undefined}>
      <div className="stack-sm">
        <div className="row-wrap" style={{ gap: 8 }}>
          {r.created && <Badge tone="green">{r.created.employees} çalışan · {r.created.tools} araç · {r.created.levels} seviye oluşturuldu</Badge>}
          <Badge tone="green">{r.imported} erişim aktarıldı</Badge>
          {r.verified !== undefined && <Badge tone="outline">{r.verified} bildirilen erişim araçta doğrulandı</Badge>}
          {r.unverified !== undefined && <Badge tone={r.unverified ? "amber" : "outline"}>{r.unverified} bildirilen API erişimi araçta bulunmadı</Badge>}
          <Badge tone="outline">{r.skipped} atlandı (zaten var)</Badge>
          <Badge tone={r.rejected.length ? "red" : "outline"}>{r.rejected.length} satır reddedildi</Badge>
          <Badge tone={r.orphans ? "amber" : "outline"}>{r.orphans} sahipsiz hesap</Badge>
          <Badge tone={r.terminated ? "amber" : "outline"}>{r.terminated} ayrılan çalışan hesabı</Badge>
        </div>
        {r.rejected.length > 0 && (
          <ul className="small" style={{ margin: "6px 0 0", paddingLeft: 18, color: "var(--red)" }}>
            {r.rejected.map((x, k) => <li key={k}>{x.line ? `Satır ${x.line}: ` : `${x.account}: `}{x.tool} · {x.level} — {rejectionText[x.reason]}</li>)}
          </ul>
        )}
        {(r.orphans > 0 || r.terminated > 0) && <p className="hint">Sahipsiz hesaplar Bilgi Güvenliğinin incelemesi için İstisnalar’a eklendi. Ayrılan çalışanlara ait hesaplar için kaldırma hemen başlatıldı. Aktarılan erişimler “kaldırılana kadar” olarak kaydedilir ve onaylayanı bilinmez.</p>}
      </div>
    </Card>
  );
}
