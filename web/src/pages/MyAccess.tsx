import { useQueryClient } from "@tanstack/react-query";
import { useState, type ReactNode } from "react";
import { useNavigate } from "react-router";
import { api } from "../api/client";
import { useMyGrants } from "../api/queries";
import type { Grant } from "../api/types";
import { reasonText } from "../i18n/reasons";
import { useSession } from "../session";
import { Button, Callout, ConfirmDialog, Drawer, EmptyState, KeyValueList, PageHeader, Tabs, TableSkeleton, Tag, ToolCell } from "../ui/components";
import { ExpiryCell, GrantBadges, MethodTag } from "../ui/domain";
import { formatDate, formatDateTime } from "../ui/format";

type Bucket = "active" | "progress" | "history";
const bucketOf = (g: Grant): Bucket => g.status === "ACTIVE" || g.status === "SUSPENDED" ? "active" : g.status === "REVOKED" || g.status === "CANCELLED" ? "history" : "progress";

/** Grant details; shared by the access, team and employee pages. */
export function GrantDrawer({ grant, onClose, footer }: { grant: Grant; onClose: () => void; footer?: ReactNode }) {
  const { timezone } = useSession();
  return (
    <Drawer open onClose={onClose} title={`${grant.tool} · ${grant.level}`} sub={grant.employee?.fullName} footer={footer}>
      <div className="stack" style={{ gap: 20 }}>
        <div className="row-wrap"><GrantBadges grant={grant} /><MethodTag method={grant.method} />{grant.retainedOnTransfer && <Tag warn>Transferde korundu</Tag>}{grant.imported && <Tag title="AccessFlow’dan önce vardı; onaylayan bilinmiyor">İçe aktarılan</Tag>}</div>
        {grant.status === "FAILED" && <Callout tone="danger" title="Erişim verilemedi">Yetkili bir yönetici işlemi İstisnalar ekranından yeniden deneyebilir.</Callout>}
        {grant.status === "REVOCATION_FAILED" && <Callout tone="danger" title="Kaldırma başarısız">Kaldırma doğrulanana kadar erişim sürmüş sayılır.</Callout>}
        <KeyValueList items={[
          ["Etkin başlangıç", grant.effectiveFrom ? formatDateTime(grant.effectiveFrom, timezone) : "Henüz etkin değil"],
          ["Bitiş", <ExpiryCell grant={grant} />],
          ["Kanıt", grant.evidence === "VERIFIED" ? "API durum sorgusuyla doğrulandı" : grant.evidence === "SELF_REPORTED" ? "Yetkili yönetici tarafından onaylandı (beyana dayalı)" : "—"],
          grant.retainedOnTransfer && ["Transferde korunma gerekçesi", grant.retainedOnTransfer.justification],
          grant.imported && ["Onay kanıtı", "AccessFlow’dan önce vardı; onaylayan bilinmiyor"],
          grant.imported && ["Kaynak", grant.toolVerifiedAt ? `Araçtan doğrulandı (${formatDateTime(grant.toolVerifiedAt, timezone)})` : "Tablo veya dosya ile bildirildi; araçtan doğrulanmadı"],
          !!grant.closedReason ? ["Kapanış nedeni", reasonText(grant.closedReason)] : null,
          !!grant.effectiveTo && ["Etkin bitiş", formatDateTime(grant.effectiveTo, timezone)],
        ]} />
      </div>
    </Drawer>
  );
}

export function MyAccessPage() {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const { timezone } = useSession();
  const query = useMyGrants();
  const [tab, setTab] = useState<Bucket>("active");
  const [open, setOpen] = useState<Grant | null>(null);
  const [revoke, setRevoke] = useState<Grant | null>(null);
  const grants = (query.data ?? []).slice().sort((a, b) => (b.effectiveFrom ?? b.createdAt).localeCompare(a.effectiveFrom ?? a.createdAt));
  const shown = grants.filter((g) => bucketOf(g) === tab);
  const count = (b: Bucket) => grants.filter((g) => bucketOf(g) === b).length;
  const actionable = (g: Grant) => g.status === "ACTIVE" && !g.suspension;
  const change = (g: Grant) => navigate(`/request-new?tool=${g.toolId}&level=${g.accessLevelId}`);

  return (
    <>
      <PageHeader title="Erişimlerim" sub="Sahip olduğunuz erişimler, durumları ve bitiş tarihleri. İhtiyacınız kalmayan bir erişimden istediğiniz zaman vazgeçebilirsiniz." />
      <Tabs value={tab} onChange={setTab} tabs={[{ value: "active", label: "Aktif", count: count("active") }, { value: "progress", label: "İşlemde", count: count("progress") }, { value: "history", label: "Geçmiş", count: count("history") }]} />
      {query.isPending ? <TableSkeleton /> : shown.length === 0 ? (
        <div className="card">
          <EmptyState icon="key" title={tab === "active" ? "Aktif erişiminiz yok" : tab === "progress" ? "İşlemde erişim yok" : "Geçmiş kayıt yok"}
            desc={tab === "active" ? "Katalogdan ihtiyacınız olan araca erişim talep edebilirsiniz." : "Sağlanan veya kaldırılan erişimler burada görünür."}
            action={tab === "active" && <Button variant="primary" onClick={() => navigate("/catalog")}>Kataloğu aç</Button>} />
        </div>
      ) : (
        <div className="card table-wrap">
          <table className="table">
            <thead><tr><th>Araç ve seviye</th><th>Durum</th><th>Başlangıç</th><th>Bitiş</th><th /></tr></thead>
            <tbody>
              {shown.map((g) => (
                <tr key={g.id} className="clickable" tabIndex={0} onClick={() => setOpen(g)} onKeyDown={(e) => e.key === "Enter" && setOpen(g)}>
                  <td><ToolCell tool={g.tool} level={g.level} /></td>
                  <td><GrantBadges grant={g} /></td>
                  <td className="nowrap small">{formatDate(g.effectiveFrom, timezone)}</td>
                  <td className="nowrap small"><ExpiryCell grant={g} /></td>
                  <td className="actions" onClick={(e) => e.stopPropagation()}>
                    {actionable(g) && (
                      <div className="row" style={{ justifyContent: "flex-end", gap: 6 }}>
                        {g.expiresOn && <Button size="sm" onClick={() => change(g)}>Süre uzat</Button>}
                        <Button size="sm" variant="danger-ghost" onClick={() => setRevoke(g)}>Vazgeç</Button>
                      </div>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {open && <GrantDrawer grant={open} onClose={() => setOpen(null)} footer={actionable(open) && <>
        <Button onClick={() => change(open)}>Seviye değiştir</Button>
        <Button variant="danger-ghost" onClick={() => { setOpen(null); setRevoke(open); }}>Erişimden vazgeç</Button>
      </>} />}
      <ConfirmDialog open={!!revoke} onClose={() => setRevoke(null)} title="Erişimden vazgeçilsin mi?"
        desc={revoke ? `${revoke.tool} · ${revoke.level} erişiminiz kaldırılır. Onay gerekmez; yeniden ihtiyaç duyarsanız yeni bir talep oluşturmanız gerekir.` : undefined}
        confirmLabel="Erişimden vazgeç" tone="danger" reason reasonLabel="Neden vazgeçiyorsunuz?" minReason={3} reasonPlaceholder="Örn. Proje tamamlandı."
        success="Kaldırma başlatıldı" successDesc="Erişim, araçta kaldırıldığı doğrulanınca kapanır."
        onConfirm={async (reason) => {
          await api.post(`/grants/${revoke!.id}/revoke`, { version: revoke!.version, reason });
          qc.invalidateQueries({ queryKey: ["access"] });
        }} />
    </>
  );
}
