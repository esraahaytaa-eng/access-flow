import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useNavigate, useParams } from "react-router";
import { api } from "../api/client";
import type { ApprovalStep, Request, RequestItem, RequestKind } from "../api/types";
import { stepLabel } from "../i18n/labels";
import { reasonText, routingNoteText } from "../i18n/reasons";
import { useSession } from "../session";
import { Button, Card, ConfirmDialog, EmptyState, Icon, KeyValueList, PageHeader, Person, Skeleton, ToolMark } from "../ui/components";
import { accountText, DurationText, ItemStateCell, RequestKindTag } from "../ui/domain";
import { formatDateTime } from "../ui/format";
import type { IconName } from "../ui/icons";

type Event = { dot: string; icon: IconName; title: string; meta?: string; note?: string | null; route?: string | null };

// Who decided the step, or while it is open, who it waits for.
const stepPerson = (s: ApprovalStep) => (s.decidedBy ? s.decidedByName : s.assigneeName) ?? "—";

export function ItemTimeline({ item, kind = "NEW_ACCESS" }: { item: RequestItem; kind?: RequestKind }) {
  const { timezone } = useSession();
  const dt = (v: string | null) => formatDateTime(v, timezone);
  const ev: Event[] = [{
    dot: "info", icon: "send", title: kind === "DIRECT_GRANT" ? "Doğrudan erişim başlatıldı" : "Talep gönderildi",
    meta: `${item.submittedBy.fullName}${item.submittedBy.id !== item.beneficiary.id ? ` · ${item.beneficiary.fullName} adına` : ""} · ${dt(item.createdAt)}`,
    note: item.justification,
  }];
  if (kind === "DIRECT_GRANT") ev.push({ dot: "info", icon: "zap", title: "Onay adımları atlandı", meta: "Süper Yönetici doğrudan erişimi · en fazla 90 gün" });
  for (const s of item.steps) {
    const L = stepLabel[s.type], who = stepPerson(s), route = s.routingNote ? routingNoteText(s.routingNote) : null;
    const e: Event =
      s.status === "APPROVED_BY_SUBMISSION" ? { dot: "done", icon: "check", title: `${L} onayı · gönderimle karşılandı`, meta: `${who} doğrudan yönetici olarak talebi gönderdi · ${dt(s.decidedAt)}` }
      : s.status === "APPROVED" ? { dot: "done", icon: "check", title: `${L} onayladı`, meta: `${who} · ${dt(s.decidedAt)}`, note: s.reason }
      : s.status === "REJECTED" ? { dot: "rejected", icon: "x", title: `${L} reddetti`, meta: `${who} · ${dt(s.decidedAt)}`, note: s.reason }
      : s.status === "SUPER_ADMIN_OVERRIDE" ? { dot: "done", icon: "zap", title: `${L} adımı Süper Yönetici tarafından geçersiz kılındı`, meta: `${s.decidedByName ?? "—"} · asıl atanan: ${s.assigneeName ?? "Bilgi güvenliği havuzu"} · ${dt(s.decidedAt)}`, note: s.reason }
      : s.status === "PENDING" ? { dot: "current", icon: "clock", title: `${L} onayı bekleniyor`, meta: `${s.assigneeName ?? "Bilgi güvenliği havuzu"} · ${dt(s.openedAt)} tarihinden beri` }
      : s.status === "CANCELLED" ? { dot: "", icon: "ban", title: `${L} adımı iptal edildi` }
      : { dot: "", icon: "dot", title: `${L} onayı`, meta: "Önceki adım tamamlanınca başlar" };
    ev.push({ ...e, route });
  }
  if (item.closedReason && (item.status === "CANCELLED" || item.status === "WITHDRAWN")) ev.push({ dot: "", icon: "ban", title: item.status === "WITHDRAWN" ? "Talep geri çekildi" : "Talep iptal edildi", meta: reasonText(item.closedReason) });
  const f = item.fulfillment;
  if (f) {
    const st = f.status.split(" / ")[0];
    ev.push(
      st === "PROVISIONING" ? { dot: "current", icon: "refresh", title: "Sağlanıyor", meta: "Araç servisine gönderildi; durum sorgusuyla doğrulanacak." }
      : st === "PENDING_FULFILLMENT" ? { dot: "current", icon: "tasks", title: "Karşılanmayı bekliyor", meta: "Yönetim grubunun kuyruğunda" }
      : st === "WAITING_VENDOR" ? { dot: "current", icon: "tasks", title: "Tedarikçi bekleniyor" }
      : st === "FAILED" ? { dot: "rejected", icon: "alert", title: "Erişim verilemedi", meta: "İşlem başarısız oldu; yetkili bir yönetici yeniden deneyebilir." }
      : st === "CANCELLED" ? { dot: "", icon: "ban", title: "Erişim iptal edildi", meta: f.closedReason ? reasonText(f.closedReason) : undefined }
      : st === "REVOCATION_REQUESTED" ? { dot: "current", icon: "ban", title: "Kaldırma talep edildi" }
      : st === "REVOCATION_FAILED" ? { dot: "rejected", icon: "ban", title: "Kaldırma başarısız" }
      : st === "REVOKED" ? { dot: "done", icon: "ban", title: "Erişim kaldırıldı", meta: f.closedReason ? reasonText(f.closedReason) : undefined }
      : { dot: "done", icon: "shieldCheck", title: f.evidence === "VERIFIED" ? "Erişim aktif · API ile doğrulandı" : "Erişim aktif · beyana dayalı onay" },
    );
  }
  return (
    <div className="timeline">
      {ev.map((e, i) => (
        <div className="tl-item" key={i}>
          <span className={`tl-dot ${e.dot}`}><Icon name={e.icon} size={11} stroke={2.4} /></span>
          <div className="tl-title">{e.title}</div>
          {e.meta && <div className="tl-meta">{e.meta}</div>}
          {e.route && <div className="tl-meta" style={{ color: "var(--blue)", marginTop: 2 }}><Icon name="swap" size={12} style={{ verticalAlign: "-2px", marginRight: 4 }} />{e.route}</div>}
          {e.note && <div className="tl-note">{e.note}</div>}
        </div>
      ))}
    </div>
  );
}

export function RequestDetailPage() {
  const { id } = useParams();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const { me, timezone } = useSession();
  const query = useQuery({ queryKey: ["requests", id], queryFn: () => api.get<Request>(`/requests/${id}`) });
  const [withdraw, setWithdraw] = useState<RequestItem | null>(null);
  const back = <Button onClick={() => navigate("/requests")}>Taleplerime dön</Button>;

  if (query.isPending) return <div className="card" style={{ padding: 18 }}><Skeleton h={20} w="40%" /></div>;
  if (query.isError) return <div className="card"><EmptyState icon="search" title="Talep bulunamadı" desc="Bu talep mevcut değil veya görüntüleme yetkiniz yok." action={back} /></div>;
  const request = query.data;
  const canWithdraw = (it: RequestItem) => it.status === "PENDING_APPROVAL" && (me.id === request.submittedBy.id || me.id === it.beneficiary.id);

  return (
    <>
      <PageHeader back={{ label: "Taleplerim", onClick: () => navigate("/requests") }}
        title={<span className="row" style={{ gap: 10 }}>Talep <span className="mono" style={{ fontSize: 20 }}>{request.id.slice(0, 8)}</span></span>}
        sub={`${formatDateTime(request.createdAt, timezone)} tarihinde gönderildi · ${request.items.length} kalem`} />
      <div className="grid-side">
        <div className="stack">
          {request.items.map((it) => (
            <Card key={it.id} title={<span className="row"><ToolMark name={it.tool} size="sm" />{it.tool} · {it.level}</span>}
              sub={<span className="row" style={{ gap: 6, marginTop: 4 }}><span className="mono">{it.id.slice(0, 8)}</span><RequestKindTag kind={request.kind} /></span>}
              actions={<ItemStateCell item={it} withSub={false} />}>
              <div className="stack" style={{ gap: 18 }}>
                <KeyValueList items={[it.account && ["GitHub hesabı", accountText(it.account)], ["Süre", <DurationText duration={it.duration} />]]} />
                <ItemTimeline item={it} kind={request.kind} />
                {(canWithdraw(it) || (["REJECTED", "CANCELLED", "WITHDRAWN"].includes(it.status) && me.id === it.beneficiary.id)) && (
                  <div className="row-wrap" style={{ justifyContent: "flex-end" }}>
                    {canWithdraw(it) && <Button variant="danger-ghost" icon="ban" onClick={() => setWithdraw(it)}>Talebi geri çek</Button>}
                    {!canWithdraw(it) && <Button icon="refresh" onClick={() => navigate(`/request-new?tool=${it.toolId}`)}>Yeni talep oluştur</Button>}
                  </div>
                )}
              </div>
            </Card>
          ))}
        </div>
        <div className="stack">
          <Card title="Talep bilgileri">
            <div className="stack">
              <div><div className="label" style={{ marginBottom: 6 }}>Hak sahibi</div><Person person={request.beneficiary} /></div>
              {request.submittedBy.id !== request.beneficiary.id && <div><div className="label" style={{ marginBottom: 6 }}>Gönderen</div><Person person={request.submittedBy} /></div>}
            </div>
          </Card>
          <Card title="Nasıl ilerler?">
            <ol className="small" style={{ margin: 0, paddingLeft: 18, color: "var(--ink-2)", display: "grid", gap: 6 }}>
              <li>Onaylar sırayla ilerler; her adıma farklı bir kişi karar verir.</li>
              <li>Tüm onaylar tamamlanınca erişim araçta oluşturulur.</li>
              <li>API araçlarında erişim, durum sorgusuyla doğrulandıktan sonra aktif sayılır. Manuel ve tedarikçi araçlarında yöneticinin beyanıyla aktifleşir.</li>
            </ol>
          </Card>
        </div>
      </div>
      <ConfirmDialog open={!!withdraw} onClose={() => setWithdraw(null)} title="Talep geri çekilsin mi?"
        desc={withdraw ? `${withdraw.tool} · ${withdraw.level} talebi iptal edilir. Bekleyen onaylar kapanır.` : undefined}
        confirmLabel="Geri çek" tone="danger" reason minReason={3} reasonPlaceholder="Örn. Artık gerekmiyor." success="Talep geri çekildi"
        onConfirm={async (reason) => {
          await api.post(`/request-items/${withdraw!.id}/withdraw`, { version: withdraw!.version, reason });
          qc.invalidateQueries({ queryKey: ["requests"] });
        }} />
    </>
  );
}
