import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api } from "../api/client";
import { useReviews } from "../api/queries";
import type { ReviewFlag, ReviewItem } from "../api/types";
import { useSession } from "../session";
import { Badge, Button, Callout, Card, ConfirmDialog, EmptyState, PageHeader, Person, SearchBox, TableSkeleton, Tabs, Tag, ToolCell, useAction } from "../ui/components";
import { formatDate } from "../ui/format";

export const flagLabel: Record<ReviewFlag, string> = {
  RETAINED_ON_TRANSFER: "Transferde korundu", EARLIER_POLICY: "Önceki politikayla onaylandı", DIRECT_GRANT: "Doğrudan erişim",
  OVERRIDE: "Süper Yönetici geçersiz kıldı", SUSPENDED: "Askıda", IMPORTED_BASELINE: "İçe aktarılan",
};
const closedLabel = { REVOKED_NO_REVIEW: "Yanıtsız; kaldırıldı", ALREADY_REMOVED: "Erişim zaten kaldırıldı" };
const DAY = 86_400_000;

type View = "mine" | "esc" | "all";
const open = (i: ReviewItem) => !i.decision && !i.closedReason;

/** D8: the reviewer decides; InfoSec decides pool items and, after day 14, any item; a Super Admin may only Revoke; never one's own access. */
function mayDecide(i: ReviewItem, decision: "KEEP" | "REVOKE", me: string, roles: { superAdmin: boolean; infosec: boolean }, escalated: boolean) {
  if (!open(i) || i.employee.id === me) return false;
  if (decision === "KEEP" && roles.superAdmin) return false;
  return i.reviewerId === me || (roles.infosec && (i.reviewerId === null || escalated || !!i.findingAt)) || (decision === "REVOKE" && roles.superAdmin);
}
const bulkEligible = (i: ReviewItem) => !i.requiresInfosec && i.flags.length === 0;

export function ReviewsPage() {
  const { me, hasRole, timezone } = useSession();
  const qc = useQueryClient();
  const query = useReviews();
  const roles = { superAdmin: hasRole("SUPER_ADMIN"), infosec: hasRole("INFOSEC") };
  const seesAll = roles.superAdmin || roles.infosec || hasRole("AUDITOR");
  const [chosen, setView] = useState<View | null>(null);
  const [search, setSearch] = useState("");
  const [selected, setSelected] = useState<string[]>([]);
  const [revoking, setRevoking] = useState<ReviewItem | null>(null);
  const [run, busy] = useAction();
  const campaign = query.data?.campaign ?? null;
  const items = query.data?.items ?? [];
  const reload = () => qc.invalidateQueries({ queryKey: ["reviews"] });

  if (query.isPending) return <><PageHeader title="Erişim incelemeleri" /><TableSkeleton /></>;
  if (!campaign && items.length === 0) {
    return <><PageHeader title="Erişim incelemeleri" /><div className="card"><EmptyState icon="review" title="Aktif inceleme kampanyası yok" desc="Kampanyalar her takvim çeyreğinin başında başlar. Başladığında araç sahipleri bilgilendirilir." /></div></>;
  }
  const escalated = !!campaign?.escalatedAt;
  const decidable = (i: ReviewItem) => mayDecide(i, "KEEP", me.id, roles, escalated) || mayDecide(i, "REVOKE", me.id, roles, escalated);
  const mine = items.filter((i) => open(i) && i.reviewerId === me.id && i.employee.id !== me.id);
  const esc = roles.infosec ? items.filter((i) => open(i) && i.reviewerId !== me.id && decidable(i)) : [];
  // Until a tab is chosen: the reviewer's own items, else what this role works on.
  const view: View = chosen ?? (mine.length || !seesAll ? "mine" : roles.infosec ? "esc" : "all");
  const tabs: { value: View; label: string; count: number }[] = [{ value: "mine", label: "İncelemem gerekenler", count: mine.length }];
  if (roles.infosec) tabs.push({ value: "esc", label: "Havuz ve eskale edilenler", count: esc.length });
  tabs.push({ value: "all", label: seesAll ? "Tüm kalemler" : "Tüm kalemlerim", count: items.length });
  const q = search.trim().toLocaleLowerCase("tr-TR");
  const shown = (view === "mine" ? mine : view === "esc" ? esc : items)
    .filter((i) => !q || `${i.employee.fullName} ${i.tool}`.toLocaleLowerCase("tr-TR").includes(q));
  const bulk = shown.filter((i) => bulkEligible(i) && mayDecide(i, "KEEP", me.id, roles, escalated));
  const done = items.filter((i) => !open(i)).length;
  const day = campaign ? Math.floor((Date.now() - Date.parse(campaign.startedAt)) / DAY) : 0;
  const autoRevoke = items.filter((i) => open(i) && i.requiresInfosec && !i.findingAt).length;

  const keep = (i: ReviewItem) => run(() => api.post(`/review-items/${i.id}/decide`, { decision: "KEEP", reason: "", version: i.version }),
    { success: "Erişim korunacak", successDesc: "Karar kayda geçti. Bitiş tarihi değişmez." }).then(reload);
  const keepSelected = () => run(() => api.post<{ kept: number; skipped: number }>("/review-items/bulk-keep",
    { items: bulk.filter((i) => selected.includes(i.id)).map((i) => ({ id: i.id, version: i.version })) }))
    .then((r) => { if (r.ok) setSelected([]); reload(); });

  return (
    <>
      <PageHeader title="Erişim incelemeleri" sub="Araç sahipleri her erişim için “Korunsun” ya da “Kaldırılsın” kararı verir. Yanıtlanmayan kalemler takvime göre takip edilir." />
      {campaign && (
        <Card style={{ marginBottom: 18 }}>
          <div className="stack" style={{ gap: 18 }}>
            <div className="between" style={{ flexWrap: "wrap" }}>
              <div>
                <div className="row" style={{ gap: 8 }}>
                  <span className="card-title" style={{ fontSize: 16 }}>{campaign.quarter ? `${campaign.quarter} çeyreklik inceleme` : "Ek inceleme kampanyası"}</span>
                  <Badge tone="blue" dot>Gün {day}</Badge>
                </div>
                <div className="muted small" style={{ marginTop: 2 }}>{campaign.startedBy ? `${campaign.startedBy.fullName} başlattı` : "Takvime göre başladı"} · {formatDate(campaign.startedAt, timezone)}</div>
              </div>
              <div style={{ minWidth: 220 }}>
                <div className="between small"><span className="muted">İlerleme</span><span className="strong">{done} / {items.length}</span></div>
                <div className="progress" style={{ marginTop: 6 }}><div style={{ width: `${items.length ? (done / items.length) * 100 : 0}%` }} /></div>
              </div>
            </div>
            <div className="day-track">
              {[[0, "Başladı"], [7, "Hatırlatma"], [14, "Bilgi Güvenliğine eskalasyon"], [21, "Otomatik kaldırma ve bulgular"]].map(([d, t], k, all) => (
                <div key={d} className={`day ${day >= (d as number) ? "done" : ""} ${day < (d as number) && (k === 0 || day >= (all[k - 1][0] as number)) ? "next" : ""}`}>
                  <span className="pin" />
                  <div className="dt">Gün {d} · {t}</div>
                  <div className="muted small">{formatDate(new Date(Date.parse(campaign.startedAt) + (d as number) * DAY).toISOString(), timezone)}</div>
                </div>
              ))}
            </div>
            {day < 21 && autoRevoke > 0 && <Callout tone="warn" title={`Gün 21’de ${autoRevoke} kalem otomatik kaldırılacak`}>Yanıtlanmamış ve seviyesi Bilgi Güvenliği onayı gerektiren erişimler gün 21’de kaldırılır. Diğer yanıtlanmamış kalemler aktif kalır ancak açık bulgu olarak İstisnalar’a düşer.</Callout>}
          </div>
        </Card>
      )}
      <Tabs value={view} onChange={(v) => { setView(v); setSelected([]); }} tabs={tabs} />
      <div className="toolbar">
        <SearchBox value={search} onChange={setSearch} placeholder="Çalışan veya araç ara" />
        {bulk.length > 0 && (
          <div className="row" style={{ marginLeft: "auto" }}>
            <span className="muted small">{selected.length} seçili</span>
            <Button size="sm" variant="primary" disabled={!selected.length} loading={busy} onClick={keepSelected}>Seçilenleri koru</Button>
          </div>
        )}
      </div>
      {roles.superAdmin && !roles.infosec && <div style={{ marginBottom: 14 }}><Callout tone="info">Süper Yönetici olarak yalnızca “Kaldırılsın” kararı verebilirsiniz. “Korunsun” kararı yalnızca araç sahibinden veya Bilgi Güvenliğinden gelir.</Callout></div>}
      {shown.length === 0 ? (
        <div className="card"><EmptyState icon="review" title={view === "mine" ? "İncelemeniz gereken kalem kalmadı" : "Bu görünümde kalem yok"} /></div>
      ) : (
        <div className="card table-wrap">
          <table className="table">
            <thead>
              <tr>
                <th style={{ width: 36 }}>{bulk.length > 0 && <input type="checkbox" aria-label="Uygun olanların tümünü seç" checked={selected.length > 0 && selected.length === bulk.length} onChange={(e) => setSelected(e.target.checked ? bulk.map((i) => i.id) : [])} />}</th>
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
                        <Badge tone={i.decision === "KEEP" ? "green" : "red"} dot>{i.decision === "KEEP" ? "Korunsun" : "Kaldırılsın"}</Badge>
                        <span className="cell-sub">{i.decidedBy?.fullName} · {i.decidedAt && formatDate(i.decidedAt, timezone)}{i.late ? " · geç" : ""}</span>
                      </div>
                    ) : i.closedReason ? <Badge tone="outline">{closedLabel[i.closedReason]}</Badge> : (
                      <div className="row" style={{ gap: 6 }}>
                        {mayDecide(i, "REVOKE", me.id, roles, escalated) && <Button size="sm" variant="danger-ghost" onClick={() => setRevoking(i)}>Kaldır</Button>}
                        {mayDecide(i, "KEEP", me.id, roles, escalated) && <Button size="sm" loading={busy} onClick={() => keep(i)}>Koru</Button>}
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
      <p className="hint" style={{ marginTop: 12 }}>Bilgi Güvenliği onaylı seviyeler ve işaretli kalemler (içe aktarılan, transferde korunan, doğrudan erişim, askıdaki, önceki politikayla onaylanan) toplu korunamaz; tek tek değerlendirilir. “Korunsun” kararı bitiş tarihini uzatmaz.</p>
      <ConfirmDialog open={!!revoking} onClose={() => setRevoking(null)} title="Erişim kaldırılsın mı?" tone="danger" confirmLabel="Kaldırılsın" reason success="Kaldırma kararı kaydedildi"
        desc={revoking ? `${revoking.employee.fullName} · ${revoking.tool} · ${revoking.level}. Kaldırma normal iş akışıyla başlar ve doğrulanınca tamamlanır.` : undefined}
        onConfirm={async (reason) => { await api.post(`/review-items/${revoking!.id}/decide`, { decision: "REVOKE", reason, version: revoking!.version }); reload(); }} />
    </>
  );
}
