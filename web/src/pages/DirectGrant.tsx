import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useNavigate } from "react-router";
import { api, ApiError, newSubmissionId } from "../api/client";
import { useEmployees, useTools } from "../api/queries";
import type { Employee, Grant, GrantsResponse, Request } from "../api/types";
import { errorMessage } from "../i18n/errors";
import { useSession } from "../session";
import { Button, Callout, Card, ConfirmDialog, EmptyState, Field, Input, PageHeader, Segmented, Select, Tabs, Textarea, ToolCell, useAction } from "../ui/components";
import { ApprovalChain, GrantBadges, MethodTag } from "../ui/domain";
import { addDays, formatDate, todayIn } from "../ui/format";

type Tab = "grant" | "revoke";

export function DirectGrantPage() {
  const [tab, setTab] = useState<Tab>("grant");
  return (
    <>
      <PageHeader title="Doğrudan erişim" sub="Acil durumlar için onay adımlarını atlayarak erişim verin veya kaldırın. Karşılama, aracın normal yöntemiyle yapılır." />
      <Tabs value={tab} onChange={setTab} tabs={[{ value: "grant", label: "Erişim ver" }, { value: "revoke", label: "Erişimi kaldır" }]} />
      {tab === "grant" ? <GrantTab /> : <RevokeTab />}
    </>
  );
}

type Form = { employeeId: string; toolId: string; levelId: string; days: string; why: string; account: string };

function GrantTab() {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const { me, timezone } = useSession();
  const employees = (useEmployees().data ?? []) as Employee[];
  const tools = (useTools().data ?? []).filter((t) => t.status !== "RETIRED");
  const [form, setForm] = useState<Form>({ employeeId: "", toolId: "", levelId: "", days: "30", why: "", account: "" });
  const [errors, setErrors] = useState<Partial<Record<keyof Form, string>>>({});
  const [submissionId] = useState(newSubmissionId);
  const [run, busy] = useAction();
  const tool = tools.find((t) => t.id === form.toolId);
  const levels = (tool?.levels ?? []).filter((l) => l.status !== "RETIRED");
  const level = levels.find((l) => l.id === form.levelId);
  const days = Number(form.days);
  const validDays = Number.isInteger(days) && days >= 2 && days <= 90;
  // The server keeps access through the end of expiresOn and allows at most 90 days from now, so N days counts today.
  const expiresOn = validDays ? addDays(todayIn(timezone), days - 1) : "";
  const set = (k: keyof Form, v: string) => { setForm((f) => ({ ...f, [k]: v, ...(k === "toolId" ? { levelId: "" } : {}) })); setErrors((e) => ({ ...e, [k]: undefined })); };

  const submit = async () => {
    const next: typeof errors = {
      employeeId: form.employeeId ? undefined : "Çalışan seçin.",
      toolId: form.toolId ? undefined : "Araç seçin.",
      levelId: form.toolId && !form.levelId ? "Seviye seçin." : undefined,
      account: tool?.accountKind && !form.account.trim() ? "GitHub kullanıcı adını girin." : undefined,
      days: validDays ? undefined : "2 ile 90 gün arasında bir süre girin.",
      why: form.why.trim().length >= 15 ? undefined : "Gerekçe en az 15 karakter olmalı.",
    };
    setErrors(next);
    if (Object.values(next).some(Boolean)) return;
    const r = await run(() => api.post<Request>("/direct-grants", {
      submissionId, beneficiaryId: form.employeeId, accessLevelId: form.levelId, justification: form.why.trim(), expiresOn,
      ...(tool?.accountKind && { account: form.account.trim() }),
    }), { success: "Doğrudan erişim başlatıldı", successDesc: "Bilgi Güvenliği ve çalışanın yöneticisi bilgilendirildi." });
    if (r.ok) { qc.invalidateQueries({ queryKey: ["requests"] }); navigate(`/requests/${r.res.id}`); }
    else if (r.err instanceof ApiError && r.err.field) {
      const key = ({ beneficiaryId: "employeeId", accessLevelId: "levelId", expiresOn: "days", justification: "why", account: "account" } as Record<string, keyof Form>)[r.err.field];
      if (key) setErrors({ [key]: errorMessage(r.err) });
    }
  };

  return (
    <div className="grid-side">
      <Card title="Doğrudan erişim ver">
        <div className="stack">
          <div className="form-grid">
            <Field label="Çalışan" required error={errors.employeeId} className="full">
              <Select value={form.employeeId} onChange={(e) => set("employeeId", e.target.value)} placeholder="Çalışan seçin"
                options={employees.filter((e) => e.status === "ACTIVE" && e.id !== me.id).map((e) => ({ value: e.id, label: e.jobTitle ? `${e.fullName} — ${e.jobTitle}` : e.fullName }))} />
            </Field>
            <Field label="Araç" required error={errors.toolId}><Select value={form.toolId} onChange={(e) => set("toolId", e.target.value)} placeholder="Araç seçin" options={tools.map((t) => ({ value: t.id, label: t.name }))} /></Field>
            <Field label="Seviye" required error={errors.levelId}><Select value={form.levelId} disabled={!tool} onChange={(e) => set("levelId", e.target.value)} placeholder={tool ? "Seviye seçin" : "Önce araç seçin"} options={levels.map((l) => ({ value: l.id, label: l.name }))} /></Field>
            {tool?.accountKind === "GITHUB_LOGIN" && (
              <Field label="GitHub kullanıcı adı" required error={errors.account} className="full" hint="Erişim bu GitHub hesabına verilir; kayıttan sonra değiştirilemez.">
                <Input value={form.account} autoComplete="off" spellCheck={false} placeholder="örn. octocat" invalid={!!errors.account} onChange={(e) => set("account", e.target.value)} />
              </Field>
            )}
            <Field label="Süre (gün)" required error={errors.days} hint={`Bugün dahil, en fazla 90 gün · son gün: ${validDays ? formatDate(expiresOn, timezone) : "—"}`}>
              <div className="row">
                <Segmented<number> label="Hazır süreler" value={days} onChange={(v) => set("days", String(v))} options={[7, 30, 90].map((v) => ({ value: v, label: String(v) }))} />
                <Input type="number" min={2} max={90} value={form.days} style={{ width: 90 }} aria-label="Gün sayısı" invalid={!!errors.days} onChange={(e) => set("days", e.target.value)} />
              </div>
            </Field>
            <Field label="Gerekçe" required error={errors.why} className="full"><Textarea rows={3} value={form.why} onChange={(e) => set("why", e.target.value)} placeholder="Örn. Üretim olayı INC-2301 için acil erişim; yönetici sözlü onay verdi." /></Field>
          </div>
          <div className="row" style={{ justifyContent: "flex-end" }}><Button variant="primary" icon="zap" loading={busy} onClick={submit}>Doğrudan erişim ver</Button></div>
        </div>
      </Card>
      <div className="stack">
        <Callout tone="warn" title="Kurallar">
          <ul style={{ margin: "4px 0 0", paddingLeft: 18, display: "grid", gap: 4 }}>
            <li>Her zaman süreli; en fazla 90 gün.</li>
            <li>Kendinize doğrudan erişim veremezsiniz.</li>
            <li>Bilgi Güvenliği, çalışanın yöneticisi ve gerekiyorsa araç sahibi bilgilendirilir.</li>
            <li>Manuel ve tedarikçi araçlarda karşılamayı siz değil, başka bir yetkili yönetici tamamlar.</li>
          </ul>
        </Callout>
        {tool && level && (
          <Card title="Atlanacak onay adımları">
            <div className="stack-sm">
              <ApprovalChain steps={level.steps} />
              <div className="row" style={{ gap: 6 }}><span className="muted small">Karşılama:</span><MethodTag method={tool.fulfillmentMethod} /></div>
            </div>
          </Card>
        )}
      </div>
    </div>
  );
}

function RevokeTab() {
  const qc = useQueryClient();
  const { me } = useSession();
  const employees = (useEmployees().data ?? []) as Employee[];
  const [employeeId, setEmployeeId] = useState("");
  const [revoke, setRevoke] = useState<Grant | null>(null);
  const grants = useQuery({ queryKey: ["access", "employee", employeeId], enabled: !!employeeId, queryFn: () => api.get<GrantsResponse>(`/employees/${employeeId}/grants`).then((r) => r.grants) });
  const rows = (grants.data ?? []).filter((g) => g.status !== "REVOCATION_REQUESTED" && g.status !== "REVOCATION_FAILED");
  const emp = employees.find((e) => e.id === employeeId);
  return (
    <Card title="Doğrudan kaldırma" sub="Seçilen erişim normal kaldırma iş akışıyla kaldırılır ve doğrulanır.">
      <div className="stack">
        <Field label="Çalışan">
          <Select value={employeeId} onChange={(e) => setEmployeeId(e.target.value)} placeholder="Çalışan seçin"
            options={employees.filter((e) => e.id !== me.id).map((e) => ({ value: e.id, label: e.status === "ACTIVE" ? e.fullName : `${e.fullName} (${e.status === "SUSPENDED" ? "askıda" : "ayrıldı"})` }))} />
        </Field>
        {employeeId && !grants.isPending && (rows.length === 0 ? <EmptyState icon="key" title="Kaldırılabilir açık erişim yok" /> : rows.map((g) => (
          <div key={g.id} className="card" style={{ padding: 12 }}>
            <div className="between"><ToolCell tool={g.tool} level={g.level} /><div className="row"><GrantBadges grant={g} /><Button size="sm" variant="danger-ghost" onClick={() => setRevoke(g)}>Kaldır</Button></div></div>
          </div>
        )))}
      </div>
      <ConfirmDialog open={!!revoke} onClose={() => setRevoke(null)} title="Erişim doğrudan kaldırılsın mı?" desc={revoke ? `${emp?.fullName} · ${revoke.tool} · ${revoke.level}` : undefined}
        confirmLabel="Kaldır" tone="danger" reason minReason={10} success="Kaldırma başlatıldı"
        onConfirm={async (reason) => { await api.post(`/grants/${revoke!.id}/revoke`, { version: revoke!.version, reason }); qc.invalidateQueries({ queryKey: ["access"] }); }} />
    </Card>
  );
}
