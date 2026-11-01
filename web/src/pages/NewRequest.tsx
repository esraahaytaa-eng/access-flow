import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { useNavigate, useSearchParams } from "react-router";
import { api, ApiError, newSubmissionId } from "../api/client";
import { useEmployees, useMyGrants, useRequests, useStarterPackages, useTeamMemberGrants, useTools } from "../api/queries";
import type { CatalogTool, Duration, Grant, Request } from "../api/types";
import { errorMessage } from "../i18n/errors";
import { stepLabel } from "../i18n/labels";
import { useRoleFlags } from "../nav";
import { useSession } from "../session";
import { Button, Callout, Card, ComboBox, Field, Icon, Input, PageHeader, Person, Segmented, Select, Tag, Textarea, ToolMark, useAction } from "../ui/components";
import { ApprovalChain, isOpenGrant, MethodTag } from "../ui/domain";
import { addDays, formatDate, todayIn } from "../ui/format";

type Days = 7 | 30 | 90 | "custom";
type Line = { key: number; toolId: string; levelId: string; durationType: "" | Duration["type"]; days: Days | null; date: string; account: string };

let lineSeq = 0;
const newLine = (toolId = "", levelId = ""): Line => ({ key: ++lineSeq, toolId, levelId, durationType: "", days: null, date: "", account: "" });

export function NewRequestPage() {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [params] = useSearchParams();
  const { me, timezone, hasRole } = useSession();
  const flags = useRoleFlags();
  const tools = (useTools().data ?? []).filter((t) => t.levels.some((l) => l.requestable)).sort((a, b) => a.name.localeCompare(b.name, "tr"));
  const query = useEmployees();
  const employees = query.data ?? [];
  // Anyone below me in the management chain, direct or not (D3 note 2026-10-30); `seen` guards a cycle.
  const managerOf = new Map(employees.map((e) => [e.id, e.managerId]));
  const below = (id: string) => { const seen = new Set<string>(); for (let m = managerOf.get(id); m && !seen.has(m); m = managerOf.get(m)) { if (m === me.id) return true; seen.add(m); } return false; };
  const reports = employees.filter((e) => below(e.id) && (!("status" in e) || e.status === "ACTIVE"));
  const others = flags.hr || flags.superAdmin ? employees.filter((e) => e.id !== me.id && (!("status" in e) || e.status === "ACTIVE")) : reports;
  const [beneficiaryId, setBeneficiaryId] = useState(params.get("for") ?? me.id);
  const [lines, setLines] = useState<Line[]>(() => [newLine(params.get("tool") ?? "", params.get("level") ?? "")]);
  const [why, setWhy] = useState("");
  const [errors, setErrors] = useState<{ lines: Record<number, string[]>; why?: string }>({ lines: {} });
  const [submissionId] = useState(newSubmissionId);
  const [run, busy] = useAction();

  const isOther = beneficiaryId !== me.id;
  const beneficiary = isOther ? employees.find((e) => e.id === beneficiaryId) : { ...me, managerId: employees.find((e) => e.id === me.id)?.managerId ?? null };
  const isDirectReport = isOther && beneficiary?.managerId === me.id;
  // D3: the direct manager's submission covers the Manager step, except for those who set reporting lines (R-3).
  const bySubmission = isDirectReport && !hasRole("HR_MANAGER", "SUPER_ADMIN");
  const manager = employees.find((e) => e.id === beneficiary?.managerId);
  // Held grants are known for oneself and one's direct reports; for anyone else the server checks.
  const myGrants = useMyGrants().data ?? [];
  const reportGrants = useTeamMemberGrants(isDirectReport ? beneficiaryId : null).data ?? [];
  const heldGrants = (isOther ? reportGrants : myGrants).filter(isOpenGrant);
  const requests = useRequests().data ?? [];
  const myPending = isOther ? [] : requests.flatMap((r) => r.items).filter((i) => i.status === "PENDING_APPROVAL" && i.beneficiary.id === me.id);

  // ?for= may name someone the caller cannot pick; fall back to oneself once the list is known.
  useEffect(() => { if (query.isSuccess && beneficiaryId !== me.id && !others.some((e) => e.id === beneficiaryId)) setBeneficiaryId(me.id); });
  // ?pkg=1 preloads the beneficiary's starter package (department, optionally job title); a suggestion only.
  const packages = useStarterPackages().data;
  const prefilled = useRef(false);
  useEffect(() => {
    if (prefilled.current || !params.get("pkg") || !packages || !tools.length || !beneficiary || !("department" in beneficiary)) return;
    prefilled.current = true;
    const picks = packages.filter((p) => p.requestable && p.department === beneficiary.department && (!p.jobTitle || p.jobTitle === beneficiary.jobTitle));
    const ls = picks.flatMap((p) => { const t = tools.find((x) => x.levels.some((l) => l.id === p.accessLevelId)); return t ? [newLine(t.id, p.accessLevelId)] : []; });
    if (ls.length) setLines(ls);
  });

  const toolOf = (ln: Line) => tools.find((t) => t.id === ln.toolId);
  const heldOf = (ln: Line): Grant | undefined => heldGrants.find((g) => g.toolId === ln.toolId);
  const kindOf = (ln: Line) => { const g = heldOf(ln); return !g || !ln.levelId ? "NEW_ACCESS" : g.accessLevelId === ln.levelId ? "EXTENSION" : "LEVEL_CHANGE"; };
  const expiresOn = (ln: Line) => ln.days === "custom" ? ln.date : ln.days ? addDays(todayIn(timezone), ln.days) : "";
  const duration = (ln: Line): Duration => ln.durationType === "TEMPORARY" ? { type: "TEMPORARY", expiresOn: expiresOn(ln) } : { type: "UNTIL_REVOKED", expiresOn: null };

  const patch = (key: number, p: Partial<Line>) => {
    setLines((ls) => ls.map((l) => (l.key === key ? { ...l, ...p } : l)));
    setErrors((e) => ({ ...e, lines: { ...e.lines, [key]: [] } }));
  };

  const validate = () => {
    const out: Record<number, string[]> = {};
    lines.forEach((ln, i) => {
      const m: string[] = [];
      const tool = toolOf(ln);
      if (!tool) m.push("Araç seçin.");
      else if (lines.slice(0, i).some((o) => o.toolId === ln.toolId)) m.push("Bu araç talepte zaten var.");
      else if (!ln.levelId) m.push("Erişim seviyesi seçin.");
      if (tool && myPending.some((p) => p.toolId === tool.id)) m.push(`${tool.name} için onay bekleyen bir talep var.`);
      const held = heldOf(ln);
      if (tool && held && held.status !== "ACTIVE") m.push(`${tool.name} için süren bir erişim işlemi var.`);
      if (held && held.status === "ACTIVE" && lines.length > 1) m.push("Süre uzatma ve seviye değişikliği tek başına gönderilir.");
      if (tool?.accountKind && kindOf(ln) === "NEW_ACCESS" && !ln.account.trim()) m.push("GitHub kullanıcı adını girin.");
      if (!ln.durationType) m.push("Süre seçin.");
      else if (ln.durationType === "TEMPORARY" && !ln.days) m.push("Bitiş tarihi seçin.");
      else if (ln.days === "custom" && (!ln.date || ln.date <= todayIn(timezone))) m.push("İleri bir bitiş tarihi seçin.");
      if (m.length) out[ln.key] = m;
    });
    const whyError = why.trim().length < 15 ? "Gerekçe en az 15 karakter olmalı. Neden ihtiyaç duyulduğunu kısaca açıklayın." : undefined;
    setErrors({ lines: out, why: whyError });
    return Object.keys(out).length === 0 && !whyError;
  };

  const submit = async () => {
    if (!validate()) return;
    const justification = why.trim();
    const change = lines.length === 1 ? heldOf(lines[0]) : undefined;
    const r = await run(
      () => change
        ? api.post<Request>(`/grants/${change.id}/change`, { submissionId, accessLevelId: lines[0].levelId, justification, duration: duration(lines[0]) })
        : api.post<Request>("/requests", { submissionId, beneficiaryId: isOther ? beneficiaryId : null, items: lines.map((ln) => ({ accessLevelId: ln.levelId, justification, duration: duration(ln), ...(toolOf(ln)?.accountKind && { account: ln.account.trim() }) })) }),
      { success: "Talebiniz gönderildi", successDesc: bySubmission ? "Yönetici adımı gönderiminizle karşılandı." : "Onaylayıcılara bildirim gönderildi." },
    );
    if (r.ok) {
      qc.invalidateQueries({ queryKey: ["requests"] });
      navigate(`/requests/${r.res.id}`);
      return;
    }
    // Field errors name the line ("items[1].accessLevelId") or the justification.
    if (r.err instanceof ApiError && r.err.field) {
      const m = /^items\[(\d+)\]\.(\w+)$/.exec(r.err.field);
      const line = m ? lines[+m[1]] : r.err.field === "justification" ? undefined : lines[0];
      if (r.err.field.endsWith("justification")) setErrors((e) => ({ ...e, why: errorMessage(r.err) }));
      else if (line) setErrors((e) => ({ ...e, lines: { ...e.lines, [line.key]: [errorMessage(r.err)] } }));
    }
  };

  const canActForOthers = others.length > 0;
  return (
    <>
      <PageHeader title="Erişim talebi" sub="İhtiyacınız olan aracı ve seviyeyi seçin, ne kadar süreyle gerektiğini ve nedenini belirtin." back={{ label: "Geri", onClick: () => navigate(-1) }} />
      <div className="grid-side">
        <div className="stack">
          {canActForOthers && (
            <Card title="Kimin için?">
              <div className="stack">
                <Segmented label="Hak sahibi" value={isOther ? "other" : "me"} onChange={(v) => setBeneficiaryId(v === "me" ? me.id : others[0].id)}
                  options={[{ value: "me", label: "Kendim için" }, { value: "other", label: flags.hr ? "Bir çalışan adına" : "Ekibimden biri adına" }]} />
                {isOther && (
                  <Field label="Çalışan" hint={bySubmission ? "Doğrudan yöneticisi olduğunuz için yönetici adımı gönderiminizle karşılanır."
                    : isDirectReport ? "Yönetici bağlarını değiştirebildiğiniz için yönetici adımını sizden bağımsız bir onaylayıcı verir. Talebin hiçbir adımına siz karar veremezsiniz."
                    : "Çalışanın yöneticisi yine onay verecek. Talebin hiçbir adımına siz karar veremezsiniz."}>
                    <Select value={beneficiaryId} onChange={(e) => setBeneficiaryId(e.target.value)} options={others.map((e) => ({ value: e.id, label: e.jobTitle ? `${e.fullName} — ${e.jobTitle}` : e.fullName }))} />
                  </Field>
                )}
              </div>
            </Card>
          )}
          {lines.map((ln, idx) => {
            const tool = toolOf(ln), held = heldOf(ln), kind = kindOf(ln), lineErrors = errors.lines[ln.key];
            return (
              <Card key={ln.key} title={lines.length > 1 ? `Araç ${idx + 1}` : "Araç ve seviye"}
                actions={lines.length > 1 && <Button size="sm" variant="ghost" icon="trash" aria-label="Bu aracı kaldır" onClick={() => setLines((ls) => ls.filter((l) => l.key !== ln.key))} />}>
                <div className="stack">
                  <Field label="Araç" required>
                    <ComboBox value={ln.toolId} invalid={!!lineErrors?.length && !tool} placeholder="Araç arayın veya seçin" empty="Eşleşen araç yok" options={tools.map((t) => ({ value: t.id, label: t.name }))} onChange={(toolId) => patch(ln.key, { toolId, levelId: "" })} />
                  </Field>
                  {tool && (
                    <div className="field">
                      <span className="label">Erişim seviyesi<span className="req">*</span></span>
                      <div className="stack-sm" role="radiogroup" aria-label="Erişim seviyesi">
                        {tool.levels.filter((l) => l.requestable).map((l) => (
                          <button key={l.id} type="button" role="radio" aria-checked={ln.levelId === l.id} className={`choice ${ln.levelId === l.id ? "on" : ""}`} onClick={() => patch(ln.key, { levelId: l.id })}>
                            <span className="radio" />
                            <span style={{ flex: 1, minWidth: 0 }}>
                              <span className="row" style={{ gap: 8 }}><span className="strong">{l.name}</span>{held?.accessLevelId === l.id && <Tag>Mevcut seviye</Tag>}{l.recommended && <Tag>Önerilen</Tag>}</span>
                              <span style={{ display: "block", marginTop: 8 }}><ApprovalChain steps={l.steps} /></span>
                            </span>
                          </button>
                        ))}
                      </div>
                    </div>
                  )}
                  {tool?.accountKind === "GITHUB_LOGIN" && kind === "NEW_ACCESS" && (
                    <Field label="GitHub kullanıcı adı" required hint="Erişim bu GitHub hesabına verilir. Onaylayıcılar hesabı görür; talep gönderildikten sonra değiştirilemez.">
                      <Input value={ln.account} autoComplete="off" spellCheck={false} placeholder="örn. octocat" onChange={(e) => patch(ln.key, { account: e.target.value })} />
                    </Field>
                  )}
                  {held && kind !== "NEW_ACCESS" && (
                    <Callout title={kind === "EXTENSION" ? "Süre uzatma talebi" : "Seviye değişikliği talebi"}>
                      {kind === "EXTENSION"
                        ? `Bu seviyeye zaten sahip${isOther ? "" : "siniz"}. Talep onaylanırsa yalnızca süre güncellenir; yeni bir erişim oluşturulmaz.`
                        : `Mevcut seviye: ${held.level}. Yeni seviye etkinleşince eski seviye kaldırılır. Yeni seviyenin onay adımları uygulanır.`}
                      {held.account && ` GitHub hesabı: @${held.account.name} (değişmez).`}
                    </Callout>
                  )}
                  <div className="field">
                    <span className="label">Süre<span className="req">*</span></span>
                    <div className="grid-2">
                      <button type="button" className={`choice ${ln.durationType === "TEMPORARY" ? "on" : ""}`} onClick={() => patch(ln.key, { durationType: "TEMPORARY", days: ln.days || 30 })}>
                        <span className="radio" /><span><span className="strong" style={{ display: "block" }}>Süreli</span><span className="muted small">Seçtiğiniz tarihte otomatik kaldırılır</span></span>
                      </button>
                      <button type="button" className={`choice ${ln.durationType === "UNTIL_REVOKED" ? "on" : ""}`} onClick={() => patch(ln.key, { durationType: "UNTIL_REVOKED", days: null })}>
                        <span className="radio" /><span><span className="strong" style={{ display: "block" }}>Kaldırılana kadar</span><span className="muted small">Üç aylık erişim denetimlerine tabidir</span></span>
                      </button>
                    </div>
                    {ln.durationType === "TEMPORARY" && (
                      <div className="row-wrap" style={{ marginTop: 4 }}>
                        <Segmented<Days> label="Süre seçimi" value={ln.days} onChange={(d) => patch(ln.key, { days: d })}
                          options={[{ value: 7, label: "7 gün" }, { value: 30, label: "30 gün" }, { value: 90, label: "90 gün" }, { value: "custom", label: "Özel tarih" }]} />
                        {ln.days === "custom"
                          ? <Input type="date" aria-label="Bitiş tarihi" style={{ width: 170 }} min={addDays(todayIn(timezone), 1)} value={ln.date} onChange={(e) => patch(ln.key, { date: e.target.value })} />
                          : <span className="muted small">Bitiş: {formatDate(expiresOn(ln), timezone)} (gün sonuna kadar)</span>}
                      </div>
                    )}
                  </div>
                  {!!lineErrors?.length && <div className="callout danger" role="alert"><Icon name="alert" size={16} /><div>{lineErrors.map((m) => <div key={m}>{m}</div>)}</div></div>}
                </div>
              </Card>
            );
          })}
          <Button icon="plus" style={{ alignSelf: "flex-start" }} onClick={() => setLines((ls) => [...ls, newLine()])}>Başka bir araç ekle</Button>
          <Card title="Gerekçe">
            <Field label="Bu erişime neden ihtiyaç var?" required error={errors.why} hint="Onaylayıcılar karar vermeden önce gerekçeyi ve süreyi görür.">
              <Textarea rows={4} value={why} invalid={!!errors.why} placeholder="Örn. Ödeme servisi göç projesinde üretim günlüklerini incelemem gerekiyor." onChange={(e) => { setWhy(e.target.value); setErrors((x) => ({ ...x, why: undefined })); }} />
            </Field>
          </Card>
        </div>
        <div className="stack" style={{ position: "sticky", top: 78 }}>
          <Card title="Özet">
            <div className="stack">
              <div><div className="label" style={{ marginBottom: 6 }}>Hak sahibi</div><Person person={beneficiary} /></div>
              <div><div className="label" style={{ marginBottom: 6 }}>Yöneticisi</div><Person person={manager} /></div>
              <div className="divider" style={{ margin: 0 }} />
              {lines.map((ln) => <Summary key={ln.key} tool={toolOf(ln)} levelId={ln.levelId} isDirectReport={bySubmission} managerName={isDirectReport ? undefined : manager?.fullName} />)}
              <Button variant="primary" size="lg" block loading={busy} onClick={submit}>Talebi gönder</Button>
              <p className="hint" style={{ textAlign: "center" }}>Onay beklerken talebinizi istediğiniz zaman geri çekebilirsiniz.</p>
            </div>
          </Card>
        </div>
      </div>
    </>
  );
}

function Summary({ tool, levelId, isDirectReport, managerName }: { tool?: CatalogTool; levelId: string; isDirectReport: boolean; managerName?: string }) {
  const level = tool?.levels.find((l) => l.id === levelId);
  if (!tool || !level) return <div className="muted small">Araç ve seviye seçildiğinde onay adımları burada görünür.</div>;
  return (
    <div className="stack-sm">
      <div className="row"><ToolMark name={tool.name} size="sm" /><div><div className="strong">{tool.name} · {level.name}</div><div className="row" style={{ gap: 6, marginTop: 2 }}><MethodTag method={tool.fulfillmentMethod} /></div></div></div>
      <ol style={{ margin: 0, paddingLeft: 18, color: "var(--ink-2)", fontSize: 13 }}>
        {level.steps.map((k) => (
          <li key={k}>{stepLabel[k]}{k === "MANAGER" && isDirectReport ? " — gönderimle karşılanır" : k === "MANAGER" && managerName ? ` — ${managerName}` : k === "TOOL_OWNER" ? ` — ${tool.owner.fullName}` : ""}</li>
        ))}
      </ol>
    </div>
  );
}
