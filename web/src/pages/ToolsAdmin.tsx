import { useState } from "react";
import { useNavigate } from "react-router";
import { useTools } from "../api/queries";
import type { CatalogStatus } from "../api/types";
import { Button, EmptyState, PageHeader, SearchBox, Segmented, TableSkeleton, ToolMark } from "../ui/components";
import { ApprovalChain, MethodTag } from "../ui/domain";

const lower = (s: string) => s.toLocaleLowerCase("tr-TR");

export function ToolsAdminPage() {
  const navigate = useNavigate();
  const query = useTools();
  const [q, setQ] = useState("");
  const [status, setStatus] = useState<CatalogStatus | "ALL">("ACTIVE");
  const rows = (query.data ?? []).filter((t) => (status === "ALL" || t.status === status) && (!q.trim() || lower(t.name).includes(lower(q.trim())))).sort((a, b) => a.name.localeCompare(b.name, "tr"));
  return (
    <>
      <PageHeader title="Araç yönetimi" sub="Araçları, erişim seviyelerini, sahiplerini, yönetim gruplarını ve her seviyenin onay adımlarını yönetin."
        actions={<Button variant="primary" icon="plus" onClick={() => navigate("/tools-admin/yeni")}>Yeni araç</Button>} />
      <div className="toolbar">
        <SearchBox value={q} onChange={setQ} placeholder="Araç ara" />
        <Segmented<CatalogStatus | "ALL"> label="Durum" value={status} onChange={setStatus} options={[{ value: "ACTIVE", label: "Kullanımda" }, { value: "RETIRED", label: "Kullanımdan kaldırılan" }, { value: "ALL", label: "Tümü" }]} />
      </div>
      {query.isPending ? <TableSkeleton /> : rows.length === 0 ? (
        <div className="card"><EmptyState icon="box" title="Araç bulunamadı" action={<Button variant="primary" onClick={() => navigate("/tools-admin/yeni")}>Yeni araç ekle</Button>} /></div>
      ) : (
        <div className="card table-wrap">
          <table className="table">
            <thead><tr><th>Araç</th><th>Karşılama</th><th>Araç sahibi</th><th>Yönetim grubu</th><th>Seviyeler ve onaylar</th></tr></thead>
            <tbody>
              {rows.map((t) => (
                <tr key={t.id} className="clickable" tabIndex={0} onClick={() => navigate(`/tools-admin/${t.id}`)} onKeyDown={(e) => e.key === "Enter" && navigate(`/tools-admin/${t.id}`)}>
                  <td><div className="toolcell"><ToolMark name={t.name} size="sm" /><div><div className="cell-main">{t.name}</div>{t.status === "RETIRED" && <div className="cell-sub">Kullanımdan kaldırıldı</div>}</div></div></td>
                  <td><MethodTag method={t.fulfillmentMethod} /></td>
                  <td className="small">{t.owner.fullName}</td>
                  <td className="small">{t.adminGroup.name}</td>
                  <td>
                    <div className="stack-sm" style={{ gap: 4 }}>
                      {t.levels.filter((l) => l.status !== "RETIRED").map((l) => <div key={l.id} className="row" style={{ gap: 8 }}><span className="small" style={{ minWidth: 110 }}>{l.name}</span><ApprovalChain steps={l.steps.map((type) => ({ type, status: "WAITING" }))} /></div>)}
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}
