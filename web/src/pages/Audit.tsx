import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { useEffect, useState, type ReactNode } from "react";
import { useSearchParams } from "react-router";
import { api } from "../api/client";
import { useEmployees, useTools } from "../api/queries";
import type {
  AccessAtResponse, AuditEventsResponse, AuditFilters, HeldGrant, HeldState, LeaverRow, PrivilegedHolder, QuarterReport, Request, RequestItem, RequestsResponse,
} from "../api/types";
import { auditActionText, auditGroups, detailsText } from "../i18n/audit";
import { requestKindLabel, roleLabel, stepLabel } from "../i18n/labels";
import { useSession } from "../session";
import {
  Badge, Button, Callout, Card, Drawer, EmptyState, Field, Icon, Input, KeyValueList, PageHeader, Pager, Person, SearchBox, Select, TableSkeleton, Tabs, Tag, ToolCell,
} from "../ui/components";
import { downloadCsv } from "../ui/csv";
import type { IconName } from "../ui/icons";
import { ItemStateCell } from "../ui/domain";
import { formatDate, formatDateTime, todayIn } from "../ui/format";
import { evidenceBadge } from "../ui/status";
import { ItemTimeline } from "./RequestDetail";
import { campaignTitle } from "./Reviews";

type Tab = "events" | "pit" | "evidence" | "reports";
const PAGE = 40;

const stateBadge: Record<HeldState, { label: string; tone: "green" | "amber" | "red" }> = {
  ACTIVE: { label: "Aktif", tone: "green" }, SUSPENDED: { label: "Askıda", tone: "amber" },
  REVOCATION_REQUESTED: { label: "Kaldırma talep edildi", tone: "amber" }, REVOCATION_FAILED: { label: "Kaldırma başarısız", tone: "red" },
};

/** Who approved a held grant: unknown for imported access, the Super Admin for a direct grant, else the decided steps. */
function approversText(g: HeldGrant) {
  if (g.imported) return "Bilinmiyor (içe aktarıldı)";
  if (g.direct) return `Doğrudan: ${g.direct.fullName}`;
  return g.approvers.map((a) => `${stepLabel[a.type]}: ${a.name}`).join(" · ") || "—";
}

const holderName = (g: HeldGrant) => g.employee?.fullName ?? g.account ?? "—";
const query = (params: Record<string, string>) => new URLSearchParams(Object.entries(params).filter(([, v]) => v !== "")).toString();

export function AuditPage() {
  const { hasRole, auditor } = useSession();
  const full = !auditor && hasRole("SUPER_ADMIN", "INFOSEC");
  const [params, setParams] = useSearchParams();
  const requested = (params.get("tab") ?? "events") as Tab;
  // An external auditor reads only the event log (O7 note 2026-10-31); the server refuses the rest.
  const tab: Tab = auditor || (requested === "reports" && !full) ? "events" : requested;
  return (
    <>
      <PageHeader title="Denetim kayıtları" sub="Kim talep etti, kim onayladı, kim verdi veya kaldırdı; hepsi gerçek zaman damgalarıyla. Kayıtlar yalnızca eklenebilir, değiştirilemez." />
      {auditor
        ? <div style={{ marginBottom: 14 }}><Callout icon="eye">Dış denetçi erişimi: yalnızca denetim kayıtlarını görüntüleyebilir, filtreleyebilir ve CSV olarak indirebilirsiniz. Başka bir ekran veya işlem açık değildir.</Callout></div>
        : !full && <div style={{ marginBottom: 14 }}><Callout icon="users">Yönetici olarak yalnızca şu an size bağlı (doğrudan ve dolaylı) çalışanların kayıtlarını görüyorsunuz.</Callout></div>}
      {!auditor && <Tabs<Tab> value={tab} onChange={(t) => setParams({ tab: t })}
        tabs={[{ value: "events", label: "Olay geçmişi" }, { value: "pit", label: "Geçmiş erişim sorgusu" }, { value: "evidence", label: "Onay kanıtı" },
          ...(full ? [{ value: "reports" as Tab, label: "Kanıt raporları" }] : [])]} />}
      {tab === "events" && <EventsTab />}
      {tab === "pit" && <AccessTab />}
      {tab === "evidence" && <EvidenceTab />}
      {tab === "reports" && full && <ReportsTab />}
    </>
  );
}

function EventsTab() {
  const { timezone } = useSession();
  // The choices come with the audit log, not from the employee directory an Auditor cannot read.
  const filters = useQuery({ queryKey: ["audit", "filters"], queryFn: () => api.get<AuditFilters>("/audit/filters") }).data;
  const [q, setQ] = useState("");
  const [group, setGroup] = useState("");
  const [actor, setActor] = useState("");
  const [tool, setTool] = useState("");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [page, setPage] = useState(1);
  const action = auditGroups.find((g) => g.value === group)?.actions.join(",") ?? "";
  const search = query({ q: q.trim(), action, actor, tool, from, to });
  useEffect(() => setPage(1), [search]);
  // Pages and totals come from the server over every matching event.
  const events = useQuery({ queryKey: ["audit", "events", search, page], placeholderData: keepPreviousData,
    queryFn: () => api.get<AuditEventsResponse>(`/audit/events?${query({ q: q.trim(), action, actor, tool, from, to, page: page > 1 ? String(page) : "" })}`) });
  const rows = events.data?.events ?? [];
  const total = events.data?.total ?? 0;
  // The server streams every matching event, not only the loaded page.
  const exportCsv = () => {
    const a = document.createElement("a");
    a.href = `/api/v1/audit/events/export${search ? `?${search}` : ""}`;
    a.download = "";
    a.click();
  };
  return (
    <>
      <div className="toolbar">
        <SearchBox value={q} onChange={setQ} placeholder="Olay, kişi veya ayrıntı ara" />
        <div style={{ width: 210 }}><Select aria-label="Olay türü" placeholder="Tüm olay türleri" value={group} onChange={(e) => setGroup(e.target.value)}
          options={auditGroups.map((g) => ({ value: g.value, label: g.label }))} /></div>
        <div style={{ width: 180 }}><Select aria-label="Yapan" placeholder="Tüm kişiler" value={actor} onChange={(e) => setActor(e.target.value)}
          options={(filters?.actors ?? []).map((e) => ({ value: e.id, label: e.fullName }))} /></div>
        <div style={{ width: 170 }}><Select aria-label="Araç" placeholder="Tüm araçlar" value={tool} onChange={(e) => setTool(e.target.value)}
          options={(filters?.tools ?? []).map((t) => ({ value: t.id, label: t.name }))} /></div>
        <Input type="date" aria-label="Başlangıç tarihi" style={{ width: 150 }} value={from} onChange={(e) => setFrom(e.target.value)} />
        <Input type="date" aria-label="Bitiş tarihi" style={{ width: 150 }} value={to} onChange={(e) => setTo(e.target.value)} />
        <Button icon="download" style={{ marginLeft: "auto" }} disabled={total === 0} onClick={exportCsv}>CSV indir</Button>
      </div>
      {events.isPending ? <TableSkeleton /> : rows.length === 0 ? (
        <div className="card"><EmptyState icon="history" title="Eşleşen kayıt yok" desc="Filtreleri değiştirmeyi deneyin." /></div>
      ) : (
        <>
          <div className="card table-wrap"><table className="table">
            <thead><tr><th>Zaman</th><th>Yapan</th><th>Olay</th><th>İlgili çalışan</th><th>Ayrıntı</th></tr></thead>
            <tbody>{rows.map((e) => (
              <tr key={e.id}>
                <td className="nowrap small">{formatDateTime(e.at, timezone)}</td>
                <td className="small nowrap">{e.actor ? e.actor.fullName : <Tag>Sistem</Tag>}</td>
                <td style={{ fontWeight: 500 }}>{auditActionText(e.action)}</td>
                <td className="small nowrap">{e.subject?.fullName ?? "—"}</td>
                <td className="small" style={{ maxWidth: 380 }}>{detailsText(e.details)}{e.reason && <div className="cell-sub">Gerekçe: {e.reason}</div>}</td>
              </tr>
            ))}</tbody>
          </table></div>
          <Pager page={events.data!.page} pageSize={events.data!.pageSize} total={total} onChange={setPage} />
          <p className="hint" style={{ marginTop: 10 }}>
            {total.toLocaleString("tr-TR")} kayıt · CSV tüm eşleşen kayıtları içerir · saatler {timezone}
          </p>
        </>
      )}
    </>
  );
}

function AccessTab() {
  const { timezone } = useSession();
  const today = todayIn(timezone);
  const employees = useEmployees().data ?? [];
  const tools = useTools().data ?? [];
  const [date, setDate] = useState(today);
  const [employee, setEmployee] = useState("");
  const [tool, setTool] = useState("");
  const [shown, setShown] = useState(PAGE);
  useEffect(() => setShown(PAGE), [date, employee, tool]);
  const search = query({ date, employee, tool });
  const access = useQuery({ queryKey: ["audit", "access", search], queryFn: () => api.get<AccessAtResponse>(`/audit/access?${search}`) });
  const held = access.data?.grants ?? [];
  const removed = access.data?.removed ?? [];
  const exportCsv = () => downloadCsv(`accessflow_erisim_${date}.csv`, [
    ["Tarih", "Çalışan", "Araç", "Seviye", "Durum", "Kanıt", "Etkin başlangıç", "Onaylayanlar"],
    ...held.map((g) => [date, holderName(g), g.tool, g.level, g.state ? stateBadge[g.state].label : "", evidenceBadge(g.evidence).label,
      formatDateTime(g.effectiveFrom, timezone), approversText(g)]),
  ]);
  return (
    <>
      <div className="toolbar">
        <Field label="Tarih"><Input type="date" value={date} max={today} style={{ width: 170 }} onChange={(e) => setDate(e.target.value || today)} /></Field>
        <Field label="Çalışan"><div style={{ width: 220 }}><Select placeholder="Tüm çalışanlar" value={employee} onChange={(e) => setEmployee(e.target.value)}
          options={[...employees].sort((a, b) => a.fullName.localeCompare(b.fullName, "tr")).map((e) => ({ value: e.id, label: e.fullName }))} /></div></Field>
        <Field label="Araç"><div style={{ width: 200 }}><Select placeholder="Tüm araçlar" value={tool} onChange={(e) => setTool(e.target.value)}
          options={tools.map((t) => ({ value: t.id, label: t.name }))} /></div></Field>
        <Button icon="download" style={{ marginLeft: "auto", alignSelf: "flex-end" }} disabled={held.length === 0} onClick={exportCsv}>CSV indir</Button>
      </div>
      <p className="hint" style={{ marginBottom: 12 }}>Erişim; API’de doğrulandığı veya manuel/tedarikçi işlemde onaylandığı andan, kaldırılması doğrulanana ya da onaylanana kadar etkin sayılır. Talep edilen veya başarısız kaldırmalar hâlâ erişim olarak sayılır. İçe aktarılan erişim, içe aktarıldığı andan itibaren gösterilir.</p>
      {access.isPending ? <TableSkeleton /> : held.length === 0 ? (
        <div className="card"><EmptyState icon="history" title={`${formatDate(date, timezone)} tarihinde etkin erişim bulunamadı`} /></div>
      ) : (
        <div className="card table-wrap"><table className="table">
          <thead><tr><th>Çalışan</th><th>Araç ve seviye</th><th>{formatDate(date, timezone)} durumu</th><th>Kanıt</th><th>Etkin başlangıç</th><th>Onaylayanlar</th></tr></thead>
          <tbody>{held.slice(0, shown).map((g) => (
            <tr key={g.grantId}>
              <td>{g.employee ? <Person person={g.employee} sub={false} /> : <span className="mono small">{g.account}</span>}</td>
              <td><ToolCell tool={g.tool} level={g.level} /></td>
              <td>{g.state && <Badge tone={stateBadge[g.state].tone} dot>{stateBadge[g.state].label}</Badge>}</td>
              <td><Badge tone={evidenceBadge(g.evidence).tone}>{evidenceBadge(g.evidence).label}</Badge>{g.confirmedBy && <div className="cell-sub">{g.confirmedBy.fullName}</div>}</td>
              <td className="small nowrap">{formatDateTime(g.effectiveFrom, timezone)}</td>
              <td className="small" style={{ maxWidth: 260 }}>{approversText(g)}</td>
            </tr>
          ))}</tbody>
        </table></div>
      )}
      {held.length > shown && <div className="row" style={{ justifyContent: "center", marginTop: 12 }}><Button onClick={() => setShown(shown + PAGE)}>Daha fazla göster ({held.length - shown})</Button></div>}
      {removed.length > 0 && (
        <Card title="Bu tarihten önceki 30 günde kaldırılanlar" style={{ marginTop: 16 }} pad={false}>
          {removed.map((g) => (
            <div key={g.grantId} className="list-row">
              <div style={{ flex: 1 }}><ToolCell tool={g.tool} level={g.level} sub={holderName(g)} /></div>
              <div className="small" style={{ textAlign: "right" }}>{formatDateTime(g.effectiveTo, timezone)}
                <div className="cell-sub">{g.removalEvidence === "VERIFIED" ? "Kaldırma doğrulandı (API)" : `Beyana dayalı${g.removedBy ? ` · ${g.removedBy.fullName}` : ""}`}</div>
              </div>
            </div>
          ))}
        </Card>
      )}
    </>
  );
}

function EvidenceTab() {
  const { timezone } = useSession();
  const employees = useEmployees().data ?? [];
  const nameOf = (id: string) => employees.find((e) => e.id === id)?.fullName ?? "";
  const [q, setQ] = useState("");
  const [open, setOpen] = useState<{ req: Request; item: RequestItem } | null>(null);
  const search = query({ q: q.trim() });
  const reqs = useQuery({ queryKey: ["audit", "requests", search], queryFn: () => api.get<RequestsResponse>(`/audit/requests?${search}`).then((r) => r.requests) });
  const rows = (reqs.data ?? []).flatMap((req) => req.items.map((item) => ({ req, item })));
  return (
    <>
      <div className="toolbar"><SearchBox value={q} onChange={setQ} placeholder="Talep no, çalışan veya araç ara" /></div>
      {reqs.isPending ? <TableSkeleton /> : rows.length === 0 ? <div className="card"><EmptyState icon="search" title="Kayıt bulunamadı" /></div> : (
        <div className="card table-wrap"><table className="table">
          <thead><tr><th>Talep</th><th>Çalışan</th><th>Araç ve seviye</th><th>Onay zinciri</th><th>Sonuç</th></tr></thead>
          <tbody>{rows.map(({ req, item }) => (
            <tr key={item.id} className="clickable" onClick={() => setOpen({ req, item })}>
              <td><span className="mono">{req.id.slice(0, 8)}</span><div className="cell-sub">{requestKindLabel[req.kind]} · {formatDate(req.createdAt, timezone)}</div></td>
              <td className="small">{item.beneficiary.fullName}</td>
              <td><ToolCell tool={item.tool} level={item.level} /></td>
              <td className="small">{req.kind === "DIRECT_GRANT" ? <Tag warn>Onaylar atlandı</Tag>
                : item.steps.map((s) => `${stepLabel[s.type]}${s.decidedBy ? `: ${nameOf(s.decidedBy)}` : ""}`).join(" → ")}</td>
              <td><ItemStateCell item={item} withSub={false} /></td>
            </tr>
          ))}</tbody>
        </table></div>
      )}
      {open && (
        <Drawer open onClose={() => setOpen(null)} title={`Onay kanıtı · ${open.req.id.slice(0, 8)}`} sub={`${open.item.tool} · ${open.item.level} · ${open.item.beneficiary.fullName}`}>
          <div className="stack" style={{ gap: 20 }}>
            <KeyValueList items={[["Kalem", <span className="mono">{open.item.id}</span>], ["Tür", requestKindLabel[open.req.kind]],
              ["Gerekli adımlar (gönderim anı)", open.item.steps.map((s) => stepLabel[s.type]).join(" → ") || "Atlandı (doğrudan erişim)"]]} />
            <ItemTimeline item={open.item} kind={open.req.kind} />
            <p className="hint">Her adıma farklı bir kişi karar verdi; hak sahibi hiçbir adıma karar vermedi. Geçersiz kılmalar, asıl onaylayıcının kararı olarak değil, Süper Yönetici kararı olarak kaydedilir.</p>
          </div>
        </Drawer>
      )}
    </>
  );
}

type ReportId = "quarter" | "priv" | "override" | "policy" | "leavers";
const reports: { id: ReportId; icon: IconName; title: string; desc: string }[] = [
  { id: "quarter", icon: "file", title: "Çeyreklik kanıt paketi", desc: "Erişim denetimi kararları, eskalasyonlar ve yanıtsız kalemlerden doğan açık bulgular." },
  { id: "priv", icon: "shield", title: "Ayrıcalıklı roller", desc: "Süper Yönetici, Bilgi Güvenliği, İK, Denetçi ve yönetim grubu üyeleri; ne zamandan beri, kim atadı." },
  { id: "override", icon: "zap", title: "Geçersiz kılmalar ve doğrudan erişimler", desc: "Onay adımlarını atlayan tüm Süper Yönetici işlemleri." },
  { id: "policy", icon: "settings", title: "Politika değişiklikleri", desc: "Erişim seviyelerinin onay adımlarında yapılan değişiklikler." },
  { id: "leavers", icon: "logout", title: "Ayrılanların erişim durumu", desc: "Ayrılan çalışanların her erişimi ve kaldırma kanıtı." },
];

function ReportsTab() {
  const [open, setOpen] = useState<ReportId | null>(null);
  const report = reports.find((r) => r.id === open);
  return (
    <>
      <div className="grid-3">
        {reports.map((r) => (
          <button type="button" key={r.id} className="tool-card" onClick={() => setOpen(r.id)}>
            <span className="tile-icon"><Icon name={r.icon} size={18} /></span>
            <div className="strong">{r.title}</div>
            <div className="desc">{r.desc}</div>
          </button>
        ))}
      </div>
      {report && <ReportDrawer id={report.id} title={report.title} desc={report.desc} onClose={() => setOpen(null)} />}
    </>
  );
}

type Rendered = { body: ReactNode; csv: (string | number | null | undefined)[][] | null };

function ReportDrawer({ id, title, desc, onClose }: { id: ReportId; title: string; desc: string; onClose: () => void }) {
  const { timezone } = useSession();
  const today = todayIn(timezone);
  const [date, setDate] = useState(today);
  const path = { quarter: "/audit/reports/quarter", priv: `/audit/reports/privileged?date=${date}`, override: "/audit/reports/overrides",
    policy: "/audit/reports/policy", leavers: "/audit/reports/leavers" }[id];
  const data = useQuery({ queryKey: ["audit", "report", path], queryFn: () => api.get<unknown>(path) });
  const dt = (v: string | null | undefined) => formatDateTime(v ?? null, timezone);
  let r: Rendered = { body: null, csv: null };
  if (data.data) {
    if (id === "quarter") r = quarterReport(data.data as QuarterReport, dt, timezone);
    if (id === "priv") r = privilegedReport((data.data as { holders: PrivilegedHolder[] }).holders, date, timezone);
    if (id === "override" || id === "policy") r = eventReport((data.data as AuditEventsResponse & { rows: AuditEventsResponse["events"] }).rows, id, dt);
    if (id === "leavers") r = leaversReport((data.data as { rows: LeaverRow[] }).rows, dt, timezone);
  }
  return (
    <Drawer open onClose={onClose} title={title} sub={desc}
      footer={<><Button onClick={onClose}>Kapat</Button>{r.csv && <Button variant="primary" icon="download" onClick={() => downloadCsv(`accessflow_${id}_${today}.csv`, r.csv!)}>CSV indir</Button>}</>}>
      {id === "priv" && <div style={{ marginBottom: 12 }}><Field label="Tarih itibarıyla"><Input type="date" value={date} max={today} style={{ width: 170 }}
        onChange={(e) => setDate(e.target.value || today)} /></Field></div>}
      {data.isPending ? <TableSkeleton rows={3} /> : r.body ?? <EmptyState title="Rapor verisi yok" />}
    </Drawer>
  );
}

const decisionText = (i: QuarterReport["items"][number]) =>
  i.decision === "KEEP" ? "Erişimin devamı onaylandı" : i.decision === "REVOKE" ? "Erişimin kaldırılmasına karar verildi"
    : i.closedReason === "REVOKED_NO_REVIEW" ? "Yanıtsız; kaldırıldı" : i.closedReason === "ALREADY_REMOVED" ? "Erişim zaten kaldırıldı"
      : i.finding ? "Açık bulgu" : "Yanıt bekliyor";

function quarterReport(q: QuarterReport, dt: (v: string | null) => string, timezone: string): Rendered {
  if (!q.campaign) return { body: null, csv: null };
  const c = q.counts;
  return {
    body: (
      <div className="stack">
        <div className="grid-3">
          {([["Toplam kalem", c.total], ["Devamı onaylandı", c.kept], ["Kaldırılmasına karar verildi", c.revoked], ["Yanıt bekliyor", c.unanswered], ["Yanıtsız; kaldırıldı", c.autoRevoked],
            ["Zaten kaldırıldı", c.alreadyRemoved], ["Devreden açık bulgu", c.openFindings], ["Önceki denetimlerden açık bulgu", c.carriedFindings]] as const).filter(([l, v]) => l !== "Yanıtsız; kaldırıldı" || v > 0).map(([l, v]) => (
            <div key={l} className="card" style={{ padding: 14 }}><div className="muted small">{l}</div><div style={{ fontSize: 22, fontWeight: 700 }}>{v}</div></div>
          ))}
        </div>
        <KeyValueList items={[["Denetim", campaignTitle(q.campaign, timezone)], ["Başlangıç", dt(q.campaign.startedAt)],
          ["Hatırlatma (gün 7)", dt(q.campaign.remindedAt)], ["Eskalasyon (gün 14)", dt(q.campaign.escalatedAt)], ["Kapanış (gün 21)", dt(q.campaign.closedAt)],
          ["Politika değişiklikleri", `${q.policyChanges}`], ["Doğrudan erişim ve geçersiz kılma", `${q.overrides}`]]} />
      </div>
    ),
    csv: [["Çalışan", "Araç", "Seviye", "İnceleyen", "Karar", "Karar veren", "Zaman", "Gerekçe", "Not"],
      ...q.items.map((i) => [i.employee, i.tool, i.level, i.reviewer ?? "Bilgi Güvenliği havuzu", decisionText(i), i.decidedBy, dt(i.decidedAt), i.reason,
        i.carriedOver ? "Önceki denetimden devreden bulgu" : i.late ? "Geç" : ""])],
  };
}

const roleText = (h: PrivilegedHolder) => h.role === "ADMIN_GROUP" ? `Yönetim grubu: ${h.group}` : roleLabel[h.role];

function privilegedReport(holders: PrivilegedHolder[], date: string, timezone: string): Rendered {
  if (holders.length === 0) return { body: null, csv: null };
  return {
    body: (
      <div className="card table-wrap"><table className="table">
        <thead><tr><th>Kişi</th><th>Rol</th><th>Başlangıç</th><th>Atayan</th></tr></thead>
        <tbody>{holders.map((h, i) => (
          <tr key={i}><td><Person person={h.employee} sub={false} /></td><td>{roleText(h)}</td>
            <td className="small">{formatDate(h.since, timezone)}</td><td className="small">{h.grantedBy?.fullName ?? "Sistem (ilk kurulum)"}</td></tr>
        ))}</tbody>
      </table></div>
    ),
    csv: [["Tarih", "Kişi", "Rol", "Başlangıç", "Atayan"],
      ...holders.map((h) => [date, h.employee.fullName, roleText(h), formatDate(h.since, timezone), h.grantedBy?.fullName ?? "Sistem (ilk kurulum)"])],
  };
}

function eventReport(rows: AuditEventsResponse["events"], id: "override" | "policy", dt: (v: string) => string): Rendered {
  if (rows.length === 0) return { body: <EmptyState icon={id === "override" ? "zap" : "settings"} title="Kayıt yok" />, csv: null };
  return {
    body: (
      <div className="card table-wrap"><table className="table">
        <thead><tr><th>Zaman</th><th>Yapan</th><th>İşlem</th><th>Çalışan</th><th>Gerekçe</th></tr></thead>
        <tbody>{rows.map((e) => (
          <tr key={e.id}><td className="small nowrap">{dt(e.at)}</td><td className="small">{e.actor?.fullName ?? "Sistem"}</td>
            <td className="small">{auditActionText(e.action)}<div className="cell-sub">{detailsText(e.details)}</div></td>
            <td className="small">{e.subject?.fullName ?? "—"}</td><td className="small">{e.reason ?? "—"}</td></tr>
        ))}</tbody>
      </table></div>
    ),
    csv: [["Zaman", "Yapan", "İşlem", "Çalışan", "Ayrıntı", "Gerekçe"],
      ...rows.map((e) => [dt(e.at), e.actor?.fullName ?? "Sistem", auditActionText(e.action), e.subject?.fullName, detailsText(e.details), e.reason])],
  };
}

function leaversReport(rows: LeaverRow[], dt: (v: string | null) => string, timezone: string): Rendered {
  if (rows.length === 0) return { body: null, csv: null };
  const removal = (l: LeaverRow) => l.effectiveTo ? `${dt(l.effectiveTo)} · ${l.removalEvidence === "VERIFIED" ? "doğrulandı" : "beyana dayalı"}`
    : l.status === "REVOCATION_FAILED" ? "Kaldırma başarısız — istisna açık" : "Bekleniyor";
  return {
    body: (
      <div className="card table-wrap"><table className="table">
        <thead><tr><th>Çalışan</th><th>Araç ve seviye</th><th>Durum</th><th>Kaldırma</th></tr></thead>
        <tbody>{rows.map((l, i) => (
          <tr key={i}><td className="small">{l.employee.fullName}<div className="cell-sub">Ayrılış: {formatDate(l.terminatedAt, timezone)}</div></td>
            <td><ToolCell tool={l.tool} level={l.level} /></td>
            <td>{l.status === "REVOKED" ? <Badge tone="green" dot>Kaldırıldı</Badge> : <Badge tone="red" dot>Açık</Badge>}</td>
            <td className="small">{removal(l)}</td></tr>
        ))}</tbody>
      </table></div>
    ),
    csv: [["Çalışan", "Ayrılış", "Araç", "Seviye", "Durum", "Kaldırma kanıtı", "Kaldırma zamanı"],
      ...rows.map((l) => [l.employee.fullName, formatDate(l.terminatedAt, timezone), l.tool, l.level, l.status === "REVOKED" ? "Kaldırıldı" : "Açık",
        l.removalEvidence === "VERIFIED" ? "Doğrulandı" : l.removalEvidence ? "Beyana dayalı" : "", dt(l.effectiveTo)])],
  };
}
