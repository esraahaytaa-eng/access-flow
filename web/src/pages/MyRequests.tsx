import { useState } from "react";
import { useNavigate } from "react-router";
import { useRequests } from "../api/queries";
import type { RequestItem } from "../api/types";
import { requestKindLabel } from "../i18n/labels";
import { useSession } from "../session";
import { Button, EmptyState, PageHeader, SearchBox, Tabs, TableSkeleton, ToolCell } from "../ui/components";
import { DurationText, ItemStateCell } from "../ui/domain";
import { relativeTime } from "../ui/format";

type Bucket = "open" | "done" | "closed";

/** Approval is not access: an approved item is done only once its grant was granted. */
export function itemBucket(it: RequestItem): Bucket {
  if (it.status === "PENDING_APPROVAL") return "open";
  if (it.status !== "APPROVED") return "closed";
  return it.fulfillment && /^(ACTIVE|SUSPENDED|REVOKED) /.test(it.fulfillment.status) ? "done" : "open";
}

const matches = (q: string, ...fields: string[]) => !q.trim() || fields.some((f) => f.toLocaleLowerCase("tr-TR").includes(q.trim().toLocaleLowerCase("tr-TR")));

export function MyRequestsPage() {
  const navigate = useNavigate();
  const { me } = useSession();
  const query = useRequests();
  const [tab, setTab] = useState<"all" | Bucket>("all");
  const [q, setQ] = useState("");
  const rows = (query.data ?? []).flatMap((r) => r.items.map((it) => ({ it, kind: r.kind }))).sort((a, b) => b.it.createdAt.localeCompare(a.it.createdAt));
  const count = (b: Bucket) => rows.filter((r) => itemBucket(r.it) === b).length;
  const shown = rows.filter((r) => (tab === "all" || itemBucket(r.it) === tab) && matches(q, r.it.tool, r.it.level, r.it.requestId, r.it.beneficiary.fullName));

  return (
    <>
      <PageHeader title="Taleplerim" sub="Kendiniz için veya başkası adına gönderdiğiniz talepler ve her birinin güncel durumu."
        actions={<Button variant="primary" icon="plus" onClick={() => navigate("/request-new")}>Yeni talep</Button>} />
      <Tabs value={tab} onChange={setTab} tabs={[
        { value: "all", label: "Tümü", count: rows.length }, { value: "open", label: "Devam eden", count: count("open") },
        { value: "done", label: "Tamamlanan", count: count("done") }, { value: "closed", label: "Reddedilen ve iptal", count: count("closed") },
      ]} />
      <div className="toolbar"><SearchBox value={q} onChange={setQ} placeholder="Araç, seviye veya talep no ara" /></div>
      {query.isPending ? <TableSkeleton /> : shown.length === 0 ? (
        <div className="card">
          <EmptyState icon="list" title={q ? "Eşleşen talep yok" : "Bu görünümde talep yok"} desc={q ? "Farklı bir arama deneyin." : "Yeni bir talep oluşturduğunuzda burada görünür."}
            action={!q && <Button variant="primary" onClick={() => navigate("/catalog")}>Kataloğu aç</Button>} />
        </div>
      ) : (
        <div className="card table-wrap">
          <table className="table">
            <thead><tr><th>Araç ve seviye</th><th>Talep</th><th>Süre</th><th>Gönderim</th><th>Durum</th></tr></thead>
            <tbody>
              {shown.map(({ it, kind }) => (
                <tr key={it.id} className="clickable" tabIndex={0} onClick={() => navigate(`/requests/${it.requestId}`)} onKeyDown={(e) => e.key === "Enter" && navigate(`/requests/${it.requestId}`)}>
                  <td><ToolCell tool={it.tool} level={it.level} sub={it.beneficiary.id !== me.id ? `${it.beneficiary.fullName} adına` : undefined} /></td>
                  <td><div className="mono">{it.requestId.slice(0, 8)}</div><div className="cell-sub">{requestKindLabel[kind]}</div></td>
                  <td className="nowrap small"><DurationText duration={it.duration} /></td>
                  <td className="nowrap muted small">{relativeTime(it.createdAt)}</td>
                  <td><ItemStateCell item={it} /></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}
