import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useNavigate, useParams } from "react-router";
import { api } from "../api/client";
import { useAdminGroups, useEmployees, useTools } from "../api/queries";
import type { CatalogTool, Employee, FulfillmentMethod, StepType, Tool } from "../api/types";
import { methodDesc, methodLabel } from "../i18n/labels";
import { Badge, Button, Callout, Card, ConfirmDialog, EmptyState, Field, Input, PageHeader, Select, Switch, TableSkeleton, useAction } from "../ui/components";
import { ApprovalChain } from "../ui/domain";
import { managerOptions } from "./Employees";

type Level = { key: string; id: string | null; name: string; owner: boolean; infosec: boolean; retire: boolean; retired: boolean; version: number | null; orig?: { name: string; owner: boolean; infosec: boolean } };
type Form = { name: string; method: FulfillmentMethod | ""; ownerId: string; adminGroupId: string; levels: Level[] };

let seq = 0;
const blankLevel = (): Level => ({ key: `new-${++seq}`, id: null, name: "", owner: false, infosec: false, retire: false, retired: false, version: null });
const stepsOf = (l: Pick<Level, "owner" | "infosec">): StepType[] => ["MANAGER", ...(l.owner ? ["TOOL_OWNER" as const] : []), ...(l.infosec ? ["INFOSEC" as const] : [])];

function formOf(tool?: CatalogTool): Form {
  if (!tool) return { name: "", method: "", ownerId: "", adminGroupId: "", levels: [blankLevel()] };
  return {
    name: tool.name, method: tool.fulfillmentMethod, ownerId: tool.owner.id, adminGroupId: tool.adminGroup.id,
    levels: tool.levels.map((l) => {
      const owner = l.steps.includes("TOOL_OWNER"), infosec = l.steps.includes("INFOSEC");
      return { key: l.id, id: l.id, name: l.name, owner, infosec, retire: false, retired: l.status === "RETIRED", version: l.version ?? null, orig: { name: l.name, owner, infosec } };
    }),
  };
}

export function ToolEditPage() {
  const { id } = useParams();
  const navigate = useNavigate();
  const tools = useTools();
  const tool = tools.data?.find((t) => t.id === id);
  if (id === "yeni") return <ToolForm />;
  if (tools.isPending) return <TableSkeleton />;
  if (!tool) return <div className="card"><EmptyState title="Araç bulunamadı" action={<Button onClick={() => navigate("/tools-admin")}>Araç yönetimine dön</Button>} /></div>;
  // Keyed by the tool and level versions, so the saved state reloads after any save, including a partly failed one.
  return <ToolForm key={[tool.version, ...tool.levels.map((l) => `${l.id}:${l.version}`)].join("|")} tool={tool} />;
}

function ToolForm({ tool }: { tool?: CatalogTool }) {
  const isNew = !tool;
  const navigate = useNavigate();
  const qc = useQueryClient();
  const employees = (useEmployees().data ?? []) as Employee[];
  const groups = useAdminGroups().data ?? [];
  const [form, setForm] = useState<Form>(() => formOf(tool));
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [confirm, setConfirm] = useState<"policy" | "retire" | null>(null);
  const [run, busy] = useAction();
  const retired = tool?.status === "RETIRED";
  const set = <K extends keyof Form>(k: K, v: Form[K]) => { setForm((f) => ({ ...f, [k]: v })); setErrors((e) => ({ ...e, [k]: "" })); };
  const setLevel = (key: string, p: Partial<Level>) => { setForm((f) => ({ ...f, levels: f.levels.map((l) => (l.key === key ? { ...l, ...p } : l)) })); setErrors((e) => ({ ...e, [`l-${key}`]: "", levels: "" })); };
  const changedSteps = form.levels.filter((l) => l.orig && !l.retired && !l.retire && (l.orig.owner !== l.owner || l.orig.infosec !== l.infosec));
  const newlyRetired = form.levels.filter((l) => l.retire);

  const validate = () => {
    const e: Record<string, string> = {};
    if (!form.name.trim()) e.name = "Araç adı zorunludur.";
    if (!form.method) e.method = "Karşılama yöntemini seçin.";
    if (!form.ownerId) e.ownerId = "Araç sahibi seçin.";
    if (!form.adminGroupId) e.adminGroupId = "Yönetim grubu seçin.";
    for (const l of form.levels) if (!l.retired && !l.retire && !l.name.trim()) e[`l-${l.key}`] = "Seviye adı zorunludur.";
    if (!form.levels.some((l) => !l.retired && !l.retire)) e.levels = "En az bir aktif erişim seviyesi olmalı.";
    setErrors(e);
    return !Object.values(e).some(Boolean);
  };

  // One call per change: the tool, then each level. A failure stops there; the refetch below reloads what was saved.
  const save = async () => {
    let toolId = tool?.id;
    const r = await run(async () => {
      if (isNew) {
        toolId = (await api.post<Tool>("/tools", { name: form.name.trim(), fulfillmentMethod: form.method, ownerId: form.ownerId, adminGroupId: form.adminGroupId })).id;
      } else if (form.name.trim() !== tool!.name || form.ownerId !== tool!.owner.id || form.adminGroupId !== tool!.adminGroup.id) {
        await api.put(`/tools/${toolId}`, { name: form.name.trim(), ownerId: form.ownerId, adminGroupId: form.adminGroupId, version: tool!.version });
      }
      for (const l of form.levels) {
        const body = { name: l.name.trim(), requiresToolOwner: l.owner, requiresInfosec: l.infosec };
        if (l.retired) continue;
        if (!l.id) await api.post(`/tools/${toolId}/levels`, body);
        else if (l.retire) await api.post(`/tools/${toolId}/levels/${l.id}/retire`, { version: l.version });
        else if (l.orig && (l.orig.name !== body.name || l.orig.owner !== l.owner || l.orig.infosec !== l.infosec)) await api.put(`/tools/${toolId}/levels/${l.id}`, { ...body, version: l.version });
      }
    }, isNew ? { success: "Araç oluşturuldu", successDesc: "Araç katalogda görünür ve talep edilebilir." } : { success: "Değişiklikler kaydedildi" });
    await qc.invalidateQueries({ queryKey: ["tools"] });
    if (r.ok) navigate("/tools-admin");
    else if (isNew && toolId) navigate(`/tools-admin/${toolId}`, { replace: true });
  };
  const submit = () => { if (validate()) (changedSteps.length || newlyRetired.length ? setConfirm("policy") : save()); };

  return (
    <>
      <PageHeader back={{ label: "Araç yönetimi", onClick: () => navigate("/tools-admin") }} title={isNew ? "Yeni araç" : form.name || "Araç"}
        sub={isNew ? "Aracı tanımlayın, karşılama yöntemini seçin ve her erişim seviyesi için gereken onay adımlarını belirleyin." : retired ? "Bu araç kullanımdan kaldırıldı; yeni talep alınmaz." : "Değişiklikler kaydedildiğinde denetim kaydına işlenir."}
        actions={!isNew && !retired && <Button variant="danger-ghost" icon="ban" onClick={() => setConfirm("retire")}>Aracı kullanımdan kaldır</Button>} />
      {retired && <div style={{ marginBottom: 16 }}><Callout tone="warn">Kullanımdan kaldırılan araçlar düzenlenemez. Onaylanmış ancak henüz araca gönderilmemiş talepler “araç kullanımdan kaldırıldı” gerekçesiyle iptal edilir.</Callout></div>}
      <fieldset disabled={retired} style={{ border: 0, padding: 0, margin: 0 }}>
        <div className="stack" style={{ maxWidth: 880 }}>
          <Card title="Genel bilgiler">
            <div className="form-grid"><Field label="Araç adı" required error={errors.name}><Input value={form.name} onChange={(e) => set("name", e.target.value)} placeholder="Örn. HubSpot" /></Field></div>
          </Card>
          <Card title="Karşılama yöntemi" sub={isNew ? "Onaylanan erişimin araçta nasıl oluşturulacağı ve kaldırılacağı." : "Yöntem araç oluşturulduktan sonra değiştirilemez."}>
            <div className="stack">
              <div className="grid-3" role="radiogroup" aria-label="Karşılama yöntemi">
                {(["API", "MANUAL", "VENDOR"] as const).map((m) => (
                  <button key={m} type="button" role="radio" aria-checked={form.method === m} disabled={!isNew} className={`choice ${form.method === m ? "on" : ""}`} onClick={() => set("method", m)}>
                    <span className="radio" /><span><span className="strong" style={{ display: "block" }}>{methodLabel[m]}</span><span className="muted small">{methodDesc[m]}</span></span>
                  </button>
                ))}
              </div>
              {errors.method && <div className="error-text">{errors.method}</div>}
            </div>
          </Card>
          <Card title="Sahiplik">
            <div className="form-grid">
              <Field label="Araç sahibi" required error={errors.ownerId} hint="Araç sahibi adımı olan seviyeleri onaylar."><Select value={form.ownerId} onChange={(e) => set("ownerId", e.target.value)} placeholder="Seçin" options={managerOptions(employees)} /></Field>
              <Field label="Yönetim grubu" required error={errors.adminGroupId} hint="Manuel ve tedarikçi görevlerini bu grubun üyeleri üstlenir; başarısız işlemleri yeniden deneyebilir.">
                <Select value={form.adminGroupId} onChange={(e) => set("adminGroupId", e.target.value)} placeholder="Seçin" options={groups.map((g) => ({ value: g.id, label: g.memberIds ? `${g.name} (${g.memberIds.length} üye)` : g.name }))} />
              </Field>
            </div>
          </Card>
          <Card title="Erişim seviyeleri ve onay adımları" sub="Yönetici onayı her zaman gereklidir. Adımlar sırayla işler: Yönetici → Araç sahibi → Bilgi güvenliği."
            actions={!retired && <Button size="sm" icon="plus" onClick={() => setForm((f) => ({ ...f, levels: [...f.levels, blankLevel()] }))}>Seviye ekle</Button>}>
            <div className="stack-sm">
              {errors.levels && <div className="error-text">{errors.levels}</div>}
              {form.levels.map((l) => {
                const off = l.retired || l.retire;
                return (
                  <div key={l.key} className="card" style={{ padding: 16, opacity: off ? 0.6 : 1 }}>
                    <div className="stack">
                      <div className="form-grid"><Field label="Seviye adı" required error={errors[`l-${l.key}`]}><Input disabled={off} value={l.name} onChange={(e) => setLevel(l.key, { name: e.target.value })} placeholder="Örn. Görüntüleyici" /></Field></div>
                      <div className="row-wrap" style={{ gap: 22 }}>
                        <label className="row" style={{ gap: 8 }}><Switch checked disabled label="Yönetici onayı" onChange={() => {}} /><span className="small">Yönetici <span className="faint">(zorunlu)</span></span></label>
                        <label className="row" style={{ gap: 8 }}><Switch checked={l.owner} disabled={off} label="Araç sahibi onayı" onChange={(v) => setLevel(l.key, { owner: v })} /><span className="small">Araç sahibi</span></label>
                        <label className="row" style={{ gap: 8 }}><Switch checked={l.infosec} disabled={off} label="Bilgi güvenliği onayı" onChange={(v) => setLevel(l.key, { infosec: v })} /><span className="small">Bilgi güvenliği</span></label>
                      </div>
                      <div className="between" style={{ flexWrap: "wrap" }}>
                        <ApprovalChain steps={stepsOf(l)} />
                        {!l.id ? form.levels.length > 1 && <Button size="sm" variant="ghost" icon="trash" onClick={() => setForm((f) => ({ ...f, levels: f.levels.filter((x) => x.key !== l.key) }))}>Kaldır</Button>
                          : l.retired ? <Badge tone="outline">Kullanımdan kaldırıldı</Badge>
                          : <Button size="sm" variant={l.retire ? "default" : "danger-ghost"} onClick={() => setLevel(l.key, { retire: !l.retire })}>{l.retire ? "Geri al" : "Kullanımdan kaldır"}</Button>}
                      </div>
                    </div>
                  </div>
                );
              })}
            </div>
          </Card>
          {!retired && <div className="row" style={{ justifyContent: "flex-end" }}><Button onClick={() => navigate("/tools-admin")}>Vazgeç</Button><Button variant="primary" loading={busy} onClick={submit}>{isNew ? "Aracı oluştur" : "Değişiklikleri kaydet"}</Button></div>}
        </div>
      </fieldset>
      <ConfirmDialog open={confirm === "policy"} onClose={() => setConfirm(null)} title="Politika değişikliğini onaylayın" confirmLabel="Kaydet ve uygula" onConfirm={save}>
        <div className="stack-sm">
          {changedSteps.map((l) => {
            const added = (l.owner && !l.orig!.owner) || (l.infosec && !l.orig!.infosec);
            return <Callout key={l.key} tone={added ? "warn" : "info"} title={`${l.name}: onay adımları değişiyor`}>{added ? "Açık erişimler “önceki politikayla onaylandı” olarak işaretlenir. Bekleyen talepler, sağlanmadan önce yeni adımı alır." : "Mevcut erişimler etkilenmez. Önceden gerekli olan adımlar süren taleplerden kaldırılmaz."}</Callout>;
          })}
          {newlyRetired.map((l) => <Callout key={l.key} tone="warn" title={`${l.name} kullanımdan kaldırılıyor`}>Yeni talep alınmaz. Onaylanmış ancak araca gönderilmemiş talepler iptal edilir; mevcut erişimler normal kaldırma süreçleriyle yönetilir.</Callout>)}
          <p className="hint">Değişiklik denetim kaydına işlenir ve Bilgi Güvenliğine bildirilir.</p>
        </div>
      </ConfirmDialog>
      <ConfirmDialog open={confirm === "retire"} onClose={() => setConfirm(null)} title={`${form.name} kullanımdan kaldırılsın mı?`} tone="danger" confirmLabel="Kullanımdan kaldır" success="Araç kullanımdan kaldırıldı"
        desc="Araç katalogdan kalkar ve yeni talep alınmaz. Onaylanmış ancak araca gönderilmemiş talepler iptal edilir. Mevcut erişimler kaldırma süreçleriyle yönetilmeye devam eder."
        onConfirm={async () => { await api.post(`/tools/${tool!.id}/retire`, { version: tool!.version }); qc.invalidateQueries({ queryKey: ["tools"] }); navigate("/tools-admin"); }} />
    </>
  );
}
