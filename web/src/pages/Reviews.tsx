import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useSearchParams } from "react-router";
import { api } from "../api/client";
import { useReviews } from "../api/queries";
import type { GrantStatus, ReviewCampaign, ReviewFlag, ReviewItem, ReviewView } from "../api/types";
import { useSession } from "../session";
import { Badge, Button, Callout, Card, ConfirmDialog, EmptyState, PageHeader, Pager, Person, SearchBox, TableSkeleton, Tabs, Tag, ToolCell, useAction } from "../ui/components";
import { formatDate, todayIn } from "../ui/format";
import type { Tone } from "../ui/status";

export const flagLabel: Record<ReviewFlag, string> = {
  RETAINED_ON_TRANSFER: "Transferde korundu", EARLIER_POLICY: "Önceki politikayla onaylandı", DIRECT_GRANT: "Doğrudan erişim",
  OVERRIDE: "Süper Yönetici geçersiz kıldı", SUSPENDED: "Askıda", IMPORTED_BASELINE: "İçe aktarılan",
};
const closedLabel = { REVOKED_NO_REVIEW: "Yanıtsız; kaldırıldı", ALREADY_REMOVED: "Erişim zaten kaldırıldı" };
/** The access's own state once its removal has started: the review decision never overrides it. */
const removalBadge: Partial<Record<GrantStatus, [string, Tone]>> = {
  REVOCATION_REQUESTED: ["Kaldırma bekleniyor", "amber"], REVOCATION_FAILED: ["Kaldırma başarısız", "red"], REVOKED: ["Kaldırıldı", "outline"], CANCELLED: ["Kaldırıldı", "outline"],
};
/** "Erişim Denetimi — 2026 4. Çeyrek": a scheduled campaign's quarter key, else the quarter it started in. */
export function campaignTitle(c: Pick<ReviewCampaign, "quarter" | "startedAt">, timeZone: string) {
  const day = todayIn(timeZone, new Date(c.startedAt));
  const [year, quarter] = c.quarter?.split("-Q") ?? [day.slice(0, 4), String(Math.ceil(+day.slice(5, 7) / 3))];
  return `Erişim Denetimi — ${year} ${quarter}. Çeyrek`;
}
const DAY = 86_400_000;

type View = ReviewView;
const open = (i: ReviewItem) => !i.decision && !i.closedReason;

/** D8: the reviewer decides; InfoSec decides pool items and, after day 14, any item; a Super Admin may only Revoke; never one's own access.
 * Access being removed has nothing left to decide, and a leaver's access is never kept (D7). */
function mayDecide(i: ReviewItem, decision: "KEEP" | "REVOKE", me: string, roles: { superAdmin: boolean; infosec: boolean }, escalated: boolean) {
  if (!open(i) || i.employee.id === me || removalBadge[i.grantStatus]) return false;
  if (decision === "KEEP" && (roles.superAdmin || i.employee.status === "TERMINATED")) return false;
  return i.reviewerId === me || (roles.infosec && (i.reviewerId === null || escalated || !!i.findingAt)) || (decision === "REVOKE" && roles.superAdmin);
}
const bulkEligible = (i: ReviewItem) => !i.requiresInfosec && i.flags.length === 0;

export function ReviewsPage() {
  const { me, hasRole, timezone } = useSession();
  const qc = useQueryClient();
  const [params] = useSearchParams();
  const roles = { superAdmin: hasRole("SUPER_ADMIN"), infosec: hasRole("INFOSEC") };
  const seesAll = roles.superAdmin || roles.infosec;
  // Until a tab is chosen the server picks: the reviewer's own items, else what this role works on.
  const [chosen, setChosen] = useState<View | null>(params.get("view") === "findings" ? "findings" : null);
  const [search, setSearchText] = useState("");
  const [page, setPageNo] = useState(1);
  const [selected, setSelected] = useState<string[]>([]);
  const [revoking, setRevoking] = useState<ReviewItem | null>(null);
  const [run, busy] = useAction();
  // Views, search and pages are served by the server over every item (PERF-004).
  const query = useReviews({ view: chosen ?? undefined, q: search.trim(), page });
  const campaign = query.data?.campaign ?? null;
  const items = query.data?.items ?? [];
  const counts = query.data?.counts;
  const reload = () => qc.invalidateQueries({ queryKey: ["reviews"] });
  const setView = (v: View) => { setChosen(v); setPageNo(1); setSelected([]); };
  const setSearch = (v: string) => { setSearchText(v); setPageNo(1); setSelected([]); };
  const setPage = (p: number) => { setPageNo(p); setSelected([]); };

  if (query.isPending || !query.data || !counts) return <><PageHeader title="Erişim Kontrolü" /><TableSkeleton /></>;
  if (!campaign && counts.all === 0) {
    return <><PageHeader title="Erişim Kontrolü" /><div className="card"><EmptyState icon="review" title="Aktif erişim denetimi yok" desc="Denetimler her takvim çeyreğinin başında başlar. Başladığında araç sahipleri bilgilendirilir." /></div></>;
  }
  const escalated = !!campaign?.escalatedAt;
  const decidable = (i: ReviewItem) => mayDecide(i, "KEEP", me.id, roles, escalated) || mayDecide(i, "REVOKE", me.id, roles, escalated);
  const view: View = query.data.view;
  const tabs: { value: View; label: string; count: number }[] = [{ value: "mine", label: "İncelemem gerekenler", count: counts.mine }];
  if (roles.infosec) tabs.push({ value: "esc", label: "Havuz ve eskale edilenler", count: counts.esc });
  tabs.push({ value: "all", label: seesAll ? "Tüm kalemler" : "Tüm kalemlerim", count: counts.all });
  if (counts.findings > 0 || view === "findings") tabs.push({ value: "findings", label: "Açık bulgular", count: counts.findings });
  const shown = items; // this page of the view, already searched on the server
  const bulk = shown.filter((i) => bulkEligible(i) && mayDecide(i, "KEEP", me.id, roles, escalated));
  const done = counts.done;
  const day = campaign ? Math.floor((Date.now() - Date.parse(campaign.startedAt)) / DAY) : 0;

  const keep = (i: ReviewItem) => run(() => api.post(`/review-items/${i.id}/decide`, { decision: "KEEP", reason: "", version: i.version }),
    { success: "Erişimin devamı onaylandı", successDesc: "Bu çeyreğin denetim kararı kayda geçti. Bitiş tarihi değişmez." }).then(reload);
  const keepSelected = () => run(() => api.post<{ kept: number; skipped: number }>("/review-items/bulk-keep",
    { items: bulk.filter((i) => selected.includes(i.id)).map((i) => ({ id: i.id, version: i.version })) }))
    .then((r) => { if (r.ok) setSelected([]); reload(); });

  return (
    <>
      <PageHeader title="Erişim Kontrolü" sub="Her çeyrekte araç sahipleri, aracındaki her erişim için “Erişim devam etsin” ya da “Erişimi kaldır” kararı verir. Yanıtlanmayan kalemler takvime göre takip edilir." />
      {campaign && (
        <Card style={{ marginBottom: 18 }}>
          <div className="stack" style={{ gap: 18 }}>
            <div className="between" style={{ flexWrap: "wrap" }}>
              <div>
                <div className="row" style={{ gap: 8 }}>
                  <span className="card-title" style={{ fontSize: 16 }}>{campaignTitle(campaign, timezone)}</span>
                  <Badge tone="blue" dot>Gün {day}</Badge>
                </div>
                <div className="muted small" style={{ marginTop: 2 }}>{campaign.startedBy ? `${campaign.startedBy.fullName} başlattı` : "Çeyrek başında otomatik başladı"} · {formatDate(campaign.startedAt, timezone)}</div>
              </div>
              <div style={{ minWidth: 220 }}>
                <div className="between small"><span className="muted">Karar verilen</span><span className="strong">{done} / {counts.all}</span></div>
                <div className="progress" style={{ marginTop: 6 }}><div style={{ width: `${counts.all ? (done / counts.all) * 100 : 0}%` }} /></div>
              </div>
            </div>
            <div className="day-track">
              {[[0, "Denetim başladı"], [7, "Araç sahiplerine hatırlatma"], [14, "Yanıtsızlar Bilgi Güvenliğine geçer"], [21, "Yanıtsızlar güvenlik bulgusu olur"]].map(([d, t], k, all) => (
                <div key={d} className={`day ${day >= (d as number) ? "done" : ""} ${day < (d as number) && (k === 0 || day >= (all[k - 1][0] as number)) ? "next" : ""}`}>
                  <span className="pin" />
                  <div className="dt">Gün {d} · {t}</div>
                  <div className="muted small">{formatDate(new Date(Date.parse(campaign.startedAt) + (d as number) * DAY).toISOString(), timezone)}</div>
                </div>
              ))}
            </div>
          </div>
        </Card>
      )}
      <Tabs value={view} onChange={setView} tabs={tabs} />
      <div className="toolbar">
        <SearchBox value={search} onChange={setSearch} placeholder="Çalışan veya araç ara" />
        {bulk.length > 0 && (
          <div className="row" style={{ marginLeft: "auto" }}>
            <span className="muted small">{selected.length} seçili</span>
            <Button size="sm" variant="primary" disabled={!selected.length} loading={busy} onClick={keepSelected}>Seçilenlerin erişimi devam etsin</Button>
          </div>
        )}
      </div>
      {roles.superAdmin && !roles.infosec && <div style={{ marginBottom: 14 }}><Callout tone="info">Süper Yönetici olarak yalnızca “Erişimi kaldır” kararı verebilirsiniz. “Erişim devam etsin” kararı yalnızca araç sahibinden veya Bilgi Güvenliğinden gelir.</Callout></div>}
      {shown.length === 0 ? (
        <div className="card"><EmptyState icon="review" title={view === "mine" ? "İncelemeniz gereken kalem kalmadı" : "Bu görünümde kalem yok"} /></div>
      ) : (
        <div className="card table-wrap">
          <table className="table">
            <thead>
              <tr>
                <th style={{ width: 36 }}>{bulk.length > 0 && <input type="checkbox" aria-label="Bu sayfada uygun olanların tümünü seç" checked={selected.length > 0 && selected.length === bulk.length} onChange={(e) => setSelected(e.target.checked ? bulk.map((i) => i.id) : [])} />}</th>
                <th>Çalışan</th><th>Araç ve seviye</th><th>Erişim</th><th>İşaretler</th><th>Karar</th>
              </tr>
            </thead>
            <tbody>
              {shown.map((i) => (
                <tr key={i.id}>
                  <td>{bulk.includes(i) && <input type="checkbox" aria-label={`${i.employee.fullName} · ${i.tool} seç`} checked={selected.includes(i.id)} onChange={(e) => setSelected((s) => e.target.checked ? [...s, i.id] : s.filter((x) => x !== i.id))} />}</td>
                  <td><Person person={i.employee} sub={`${i.employee.jobTitle ?? ""}${i.employee.status !== "ACTIVE" ? ` · ${i.employee.status === "SUSPENDED" ? "askıda" : "ayrıldı"}` : ""}`} /></td>
                  <td><ToolCell tool={i.tool} level={i.level} sub={i.reviewerName ? `İnceleyen: ${i.reviewerName}` : "Bilgi Güvenliği havuzu"} /></td>
                  <td className="small">
                    <div>{i.expiresOn ? `${formatDate(i.expiresOn, timezone)} tarihine kadar` : "Kaldırılana kadar"}</div>
                    <div className="cell-sub">{i.effectiveFrom ? `${Math.floor((Date.now() - Date.parse(i.effectiveFrom)) / DAY)} gündür` : "—"}</div>
                  </td>
                  <td>
                    <div className="row-wrap" style={{ gap: 4 }}>
                      {i.requiresInfosec && <Tag warn>Bilgi güvenliği onaylı</Tag>}
                      {i.flags.map((f) => <Tag key={f}>{flagLabel[f]}</Tag>)}
                      {i.findingAt && <Tag warn>Açık bulgu</Tag>}
                    </div>
                  </td>
                  <td className="nowrap">
                    {i.decision ? (
                      <div className="stack-sm" style={{ gap: 2 }}>
                        <Badge tone={i.decision === "KEEP" ? "green" : "red"} dot>{i.decision === "KEEP" ? "Erişimin devamı onaylandı" : "Erişimin kaldırılmasına karar verildi"}</Badge>
                        <span className="cell-sub">{i.decidedBy?.fullName} · {i.decidedAt && formatDate(i.decidedAt, timezone)}{i.late ? " · geç" : ""}</span>
                        {removalBadge[i.grantStatus] && <RemovalBadge status={i.grantStatus} />}
                      </div>
                    ) : i.closedReason ? <Badge tone="outline">{closedLabel[i.closedReason]}</Badge> : removalBadge[i.grantStatus] ? <RemovalBadge status={i.grantStatus} /> : (
                      <div className="row" style={{ gap: 6 }}>
                        {mayDecide(i, "REVOKE", me.id, roles, escalated) && <Button size="sm" variant="danger-ghost" onClick={() => setRevoking(i)}>Erişimi kaldır</Button>}
                        {mayDecide(i, "KEEP", me.id, roles, escalated) && <Button size="sm" loading={busy} onClick={() => keep(i)}>Erişim devam etsin</Button>}
                        {!decidable(i) && <span className="muted small">{escalated ? "Eskale edildi" : "Bekliyor"}</span>}
                      </div>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <Pager page={query.data.page} pageSize={query.data.pageSize} total={query.data.total} onChange={setPage} />
      <p className="hint" style={{ marginTop: 12 }}>Bilgi Güvenliği onaylı seviyeler ve işaretli kalemler (içe aktarılan, transferde korunan, doğrudan erişim, askıdaki, önceki politikayla onaylanan) toplu onaylanamaz; tek tek değerlendirilir. “Erişim devam etsin” yalnızca bu çeyreğin denetim kararıdır: bitiş tarihini uzatmaz ve çalışan ayrılırsa erişimin kaldırılmasını engellemez.</p>
      <ConfirmDialog open={!!revoking} onClose={() => setRevoking(null)} title="Erişim kaldırılsın mı?" tone="danger" confirmLabel="Erişimi kaldır" reason success="Kaldırma kararı kaydedildi"
        desc={revoking ? `${revoking.employee.fullName} · ${revoking.tool} · ${revoking.level}. Kaldırma normal iş akışıyla başlar ve doğrulanınca tamamlanır.` : undefined}
        onConfirm={async (reason) => { await api.post(`/review-items/${revoking!.id}/decide`, { decision: "REVOKE", reason, version: revoking!.version }); reload(); }} />
    </>
  );
}

function RemovalBadge({ status }: { status: GrantStatus }) {
  const [label, tone] = removalBadge[status]!;
  return <Badge tone={tone} dot>{label}</Badge>;
}
