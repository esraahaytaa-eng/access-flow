import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api } from "../api/client";
import { useApprovals, useEmployees, useNominations } from "../api/queries";
import type { Approval, ApprovalsResponse, Employee, StepType, SuperAdminNomination } from "../api/types";
import { stepLabel } from "../i18n/labels";
import { routingNoteText } from "../i18n/reasons";
import { useSession } from "../session";
import {
  Button, Callout, Card, ConfirmDialog, Drawer, EmptyState, Field, KeyValueList, Modal, PageHeader, Person, SearchBox, Segmented, Select, Tabs, TableSkeleton, Textarea, ToolCell, useAction,
} from "../ui/components";
import { DurationText } from "../ui/domain";
import { formatDate, sinceText } from "../ui/format";
import { ItemTimeline } from "./RequestDetail";

const decidedStatuses = ["APPROVED", "APPROVED_BY_SUBMISSION", "SUPER_ADMIN_OVERRIDE"];

const lower = (s: string) => s.toLocaleLowerCase("tr-TR");

function NominationRow({ n }: { n: SuperAdminNomination }) {
  const qc = useQueryClient();
  const { timezone } = useSession();
  const [run, busy] = useAction();
  const decide = async (authorize: boolean) => {
    const r = await run(() => api.post(`/super-admin-nominations/${n.id}/${authorize ? "authorize" : "decline"}`, { version: n.version }),
      { success: authorize ? "Süper Yönetici yetkilendirildi" : "Öneri reddedildi" });
    if (r.ok) ["nominations", "admin-health", "employees"].forEach((k) => qc.invalidateQueries({ queryKey: [k] }));
  };
  return (
    <div className="list-row">
      <div style={{ flex: 1 }}><Person person={n.nominee} sub={`Öneren: ${n.proposedBy.fullName}${n.succession ? " (İK ataması)" : n.recovery ? " (kurtarma)" : ""} · ${formatDate(n.createdAt, timezone)} · ${formatDate(n.expiresAt, timezone)} tarihinde sona erer`} /></div>
      <Button size="sm" variant="danger-ghost" loading={busy} onClick={() => decide(false)}>Reddet</Button>
      <Button size="sm" variant="primary" loading={busy} onClick={() => decide(true)}>Yetkilendir</Button>
    </div>
  );
}

export function ApprovalsPage() {
  const { hasRole } = useSession();
  const oversight = hasRole("INFOSEC", "SUPER_ADMIN");
  const mine = useApprovals();
  const nominations = (useNominations(hasRole("INFOSEC", "SUPER_ADMIN")).data ?? []).filter((n) => n.canAuthorize);
  const all = useQuery({ queryKey: ["approvals", "open"], enabled: oversight, queryFn: () => api.get<ApprovalsResponse>("/approvals/open").then((r) => r.approvals) });
  const [tab, setTab] = useState<"mine" | "all">("mine");
  const [kind, setKind] = useState<"ALL" | StepType>("ALL");
  const [q, setQ] = useState("");
  const [openId, setOpenId] = useState<string | null>(null);
  const source = tab === "mine" ? mine : all;
  const rows = (source.data ?? [])
    .filter((a) => kind === "ALL" || a.step.type === kind)
    .filter((a) => !q.trim() || [a.item.beneficiary.fullName, a.item.tool, a.item.level, a.item.requestId].some((f) => lower(f).includes(lower(q.trim()))))
    .sort((a, b) => (a.step.openedAt ?? "").localeCompare(b.step.openedAt ?? ""));
  const open = [...(mine.data ?? []), ...(all.data ?? [])].find((a) => a.step.id === openId);
  const canDecide = !!open && (mine.data ?? []).some((a) => a.step.id === open.step.id);

  return (
    <>
      <PageHeader title="Onay kutusu" sub="Kararınızı bekleyen erişim talepleri. Her talepte gerekçeyi, süreyi ve önceki kararları görebilirsiniz. Tüm kararlar elle verilir." />
      {nominations.length > 0 && (
        <Card title="Yetkilendirmenizi bekleyen Süper Yönetici önerileri" sub="Süper Yönetici ataması iki kişilik onay gerektirir." style={{ marginBottom: 18 }} pad={false}>
          {nominations.map((n) => <NominationRow key={n.id} n={n} />)}
        </Card>
      )}
      <Tabs value={tab} onChange={setTab} tabs={[
        { value: "mine", label: "Bana atanan", count: mine.data?.length ?? 0 },
        ...(oversight ? [{ value: "all" as const, label: "Tüm bekleyen adımlar", count: all.data?.length ?? 0 }] : []),
      ]} />
      <div className="toolbar"><SearchBox value={q} onChange={setQ} placeholder="Çalışan, araç veya talep ara" /><Segmented<"ALL" | StepType> label="Adım türü" value={kind} onChange={setKind} options={[{ value: "ALL", label: "Tümü" }, { value: "MANAGER", label: "Yönetici" }, { value: "TOOL_OWNER", label: "Araç sahibi" }, { value: "INFOSEC", label: "Bilgi güvenliği" }]} /></div>
      {source.isPending ? <TableSkeleton /> : rows.length === 0 ? (
        <div className="card"><EmptyState icon="inbox" title={tab === "mine" ? "Bekleyen onayınız yok" : "Bekleyen adım yok"} desc={tab === "mine" ? "Size atanan yeni bir onay adımı olduğunda burada görünür ve bildirim alırsınız." : undefined} /></div>
      ) : (
        <div className="card table-wrap">
          <table className="table">
            <thead><tr><th>Çalışan</th><th>Araç ve seviye</th><th>Adım</th><th>Süre</th><th>Bekleme</th></tr></thead>
            <tbody>
              {rows.map((a) => {
                const overdue = !!a.step.openedAt && Date.now() - new Date(a.step.openedAt).getTime() > 2 * 864e5;
                const rerouted = a.step.routingNote && routingNoteText(a.step.routingNote);
                return (
                  <tr key={a.step.id} className="clickable" tabIndex={0} onClick={() => setOpenId(a.step.id)} onKeyDown={(e) => e.key === "Enter" && setOpenId(a.step.id)}>
                    <td><Person person={a.item.beneficiary} sub={false} /></td>
                    <td><ToolCell tool={a.item.tool} level={a.item.level} sub={<span className="mono">{a.item.requestId.slice(0, 8)}</span>} /></td>
                    <td>
                      <div className="stack-sm" style={{ gap: 2 }}>
                        <span>{stepLabel[a.step.type]}</span>
                        {tab === "all" && <span className="cell-sub">{a.unroutable ? <span style={{ color: "var(--red)" }}>Uygun onaylayıcı yok</span> : a.step.assigneeName ?? "Bilgi güvenliği havuzu"}</span>}
                        {rerouted && <span className="cell-sub" style={{ color: "var(--blue)" }}>Yeniden yönlendirildi</span>}
                      </div>
                    </td>
                    <td className="small nowrap"><DurationText duration={a.item.duration} /></td>
                    <td className="nowrap">{a.step.openedAt && <span className={overdue ? "strong" : ""} style={{ color: overdue ? "var(--amber)" : undefined }}>{sinceText(a.step.openedAt)}</span>}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
      {open && <DecisionDrawer approval={open} canDecide={canDecide} onClose={() => setOpenId(null)} />}
    </>
  );
}

export function DecisionDrawer({ approval, canDecide, onClose }: { approval: Approval; canDecide: boolean; onClose: () => void }) {
  const { me, hasRole } = useSession();
  const qc = useQueryClient();
  const { step, item } = approval;
  const employees = useEmployees().data ?? [];
  const beneficiary = employees.find((e) => e.id === item.beneficiary.id);
  const manager = employees.find((e) => e.id === beneficiary?.managerId);
  const [note, setNote] = useState("");
  const [noteError, setNoteError] = useState("");
  const [reassignOpen, setReassignOpen] = useState(false);
  const [overrideOpen, setOverrideOpen] = useState(false);
  const [run, busy] = useAction();
  // Mirrors the server's separation of duties: beneficiary, submitter and whoever decided an earlier step.
  const party = item.beneficiary.id === me.id || item.submittedBy.id === me.id || item.steps.some((s) => s.decidedBy === me.id && decidedStatuses.includes(s.status));
  const done = () => { qc.invalidateQueries({ queryKey: ["approvals"] }); onClose(); };
  const route = step.routingNote && routingNoteText(step.routingNote);

  const decide = async (approve: boolean) => {
    if (!approve && note.trim().length < 5) { setNoteError("Ret gerekçesi en az 5 karakter olmalı."); return; }
    const r = await run(
      () => approve ? api.post(`/approvals/${step.id}/approve`, { version: step.version }) : api.post(`/approvals/${step.id}/reject`, { version: step.version, reason: note.trim() }),
      approve ? { success: "Onaylandı", successDesc: "Talep bir sonraki adıma geçti." } : { success: "Reddedildi", successDesc: "Talep sahibine bildirildi." },
    );
    if (r.ok) done();
  };

  return (
    <Drawer open onClose={onClose} title={`${item.tool} · ${item.level}`} sub={<span className="mono">{item.requestId.slice(0, 8)}</span>}
      footer={<>
        {hasRole("INFOSEC", "SUPER_ADMIN") && !party && <Button icon="swap" onClick={() => setReassignOpen(true)}>Yeniden ata</Button>}
        {hasRole("SUPER_ADMIN") && !party && <Button icon="zap" onClick={() => setOverrideOpen(true)}>Geçersiz kıl</Button>}
        <span style={{ flex: 1 }} />
        {canDecide ? <><Button variant="danger-ghost" loading={busy} onClick={() => decide(false)}>Reddet</Button><Button variant="primary" icon="check" loading={busy} onClick={() => decide(true)}>Onayla</Button></> : <Button onClick={onClose}>Kapat</Button>}
      </>}>
      <div className="stack" style={{ gap: 20 }}>
        {canDecide && <Callout title={`${stepLabel[step.type]} olarak kararınız bekleniyor`}>{step.openedAt ? `${sinceText(step.openedAt)} bekliyor. ` : ""}Onayladığınızda talep bir sonraki adıma geçer; reddederseniz talep kapanır.</Callout>}
        {approval.unroutable && <Callout tone="danger" title="Uygun onaylayıcı yok">Bu adıma karar verebilecek bir Bilgi Güvenliği üyesi yok. Adımı yeniden atayın.</Callout>}
        {route && <Callout icon="swap">{route}</Callout>}
        <div className="grid-2">
          <div><div className="label" style={{ marginBottom: 6 }}>Hak sahibi</div><Person person={item.beneficiary} sub={[beneficiary?.jobTitle, beneficiary?.department].filter(Boolean).join(" · ") || false} /></div>
          <div><div className="label" style={{ marginBottom: 6 }}>Yöneticisi</div><Person person={manager} /></div>
        </div>
        <KeyValueList items={[["Süre", <DurationText duration={item.duration} />], ["Gönderen", item.submittedBy.id === item.beneficiary.id ? "Çalışanın kendisi" : item.submittedBy.fullName]]} />
        <div><div className="label" style={{ marginBottom: 6 }}>Gerekçe</div><div className="tl-note" style={{ marginTop: 0 }}>{item.justification}</div></div>
        <div><div className="section-title">Onay geçmişi</div><ItemTimeline item={item} /></div>
        {canDecide && (
          <Field label="Ret gerekçesi" hint="Yalnızca reddederken zorunludur. Talep sahibi ve denetçiler görür." error={noteError}>
            <Textarea rows={3} value={note} invalid={!!noteError} placeholder="Kararınızın gerekçesi" onChange={(e) => { setNote(e.target.value); setNoteError(""); }} />
          </Field>
        )}
      </div>
      {reassignOpen && <ReassignModal approval={approval} employees={employees as Employee[]} onClose={() => setReassignOpen(false)} onDone={done} />}
      <ConfirmDialog open={overrideOpen} onClose={() => setOverrideOpen(false)} title="Onay adımı geçersiz kılınsın mı?"
        desc={`${stepLabel[step.type]} adımı sizin adınıza kaydedilir; asıl onaylayıcının kararı gibi gösterilmez. Bilgi Güvenliği, çalışanın yöneticisi ve atlanan onaylayıcı bilgilendirilir.`}
        confirmLabel="Geçersiz kıl" tone="danger" reason minReason={10} success="Adım geçersiz kılındı"
        onConfirm={async (reason) => { await api.post(`/approvals/${step.id}/override`, { version: step.version, reason }); done(); }} />
    </Drawer>
  );
}

function ReassignModal({ approval, employees, onClose, onDone }: { approval: Approval; employees: Employee[]; onClose: () => void; onDone: () => void }) {
  const { step, item } = approval;
  const [to, setTo] = useState("");
  const [why, setWhy] = useState("");
  const [errors, setErrors] = useState<{ to?: string; why?: string }>({});
  const [run, busy] = useAction();
  const deciders = item.steps.map((s) => s.decidedBy);
  const options = employees
    .filter((e) => e.status === "ACTIVE" && ![item.beneficiary.id, item.submittedBy.id, step.assigneeId, ...deciders].includes(e.id))
    .filter((e) => step.type !== "INFOSEC" || e.roles.includes("INFOSEC"))
    .sort((a, b) => Number(b.roles.includes("INFOSEC")) - Number(a.roles.includes("INFOSEC")) || a.fullName.localeCompare(b.fullName, "tr"));
  const submit = async () => {
    const next = { to: to ? undefined : "Yeni onaylayıcıyı seçin.", why: why.trim().length < 5 ? "Gerekçe en az 5 karakter olmalı." : undefined };
    setErrors(next);
    if (next.to || next.why) return;
    const r = await run(() => api.post(`/approvals/${step.id}/reassign`, { version: step.version, assigneeId: to, reason: why.trim() }), { success: "Adım yeniden atandı" });
    if (r.ok) { onClose(); onDone(); }
  };
  return (
    <Modal open onClose={onClose} title="Onay adımını yeniden ata" desc={`${stepLabel[step.type]} adımı · şu an: ${step.assigneeName ?? "atanmamış"}`}
      footer={<><Button onClick={onClose}>Vazgeç</Button><Button variant="primary" loading={busy} onClick={submit}>Yeniden ata</Button></>}>
      <div className="stack">
        <Field label="Yeni onaylayıcı" required error={errors.to} hint="Hak sahibi, gönderen ve bu talepte karar vermiş kişiler listelenmez.">
          <Select value={to} onChange={(e) => setTo(e.target.value)} placeholder="Kişi seçin"
            options={options.map((e) => ({ value: e.id, label: `${e.fullName}${e.jobTitle ? ` — ${e.jobTitle}` : ""}${e.roles.includes("INFOSEC") ? " (Bilgi Güvenliği)" : ""}` }))} />
        </Field>
        <Field label="Gerekçe" required error={errors.why}><Textarea rows={3} value={why} onChange={(e) => setWhy(e.target.value)} placeholder="Örn. Araç sahibi iki hafta izinde." /></Field>
      </div>
    </Modal>
  );
}
