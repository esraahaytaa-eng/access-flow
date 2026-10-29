import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useNavigate } from "react-router";
import { api } from "../api/client";
import { useEmployees, useTeamGrants } from "../api/queries";
import type { BasicEmployee, Grant } from "../api/types";
import { useSession } from "../session";
import { Badge, Button, ConfirmDialog, Drawer, EmptyState, PageHeader, Person, SearchBox, TableSkeleton, Tag, ToolCell } from "../ui/components";
import { ExpiryCell, GrantBadges, isOpenGrant } from "../ui/domain";
import { daysUntil } from "../ui/format";

const lower = (s: string) => s.toLocaleLowerCase("tr-TR");
const removable = (g: Grant) => ["ACTIVE", "FAILED", "PENDING_FULFILLMENT", "PROVISIONING", "SUSPENDED"].includes(g.status);

export function TeamPage() {
  const { me, timezone } = useSession();
  const employees = useEmployees();
  const grantsQuery = useTeamGrants();
  const [q, setQ] = useState("");
  const [openId, setOpenId] = useState<string | null>(null);
  const reports = (employees.data ?? []).filter((e) => e.managerId === me.id && (!("status" in e) || e.status !== "TERMINATED"));
  const rows = reports.filter((e) => !q.trim() || [e.fullName, e.jobTitle ?? ""].some((f) => lower(f).includes(lower(q.trim()))));
  const grantsOf = (id: string) => (grantsQuery.data ?? []).filter((g) => g.employee.id === id && isOpenGrant(g));
  const open = reports.find((e) => e.id === openId);

  return (
    <>
      <PageHeader title="Ekibim" sub="Doğrudan bağlı çalışanlarınızın erişimleri. İhtiyaç kalmayan erişimleri istediğiniz zaman kaldırabilir, ekibiniz adına erişim talep edebilirsiniz." />
      <div className="toolbar"><SearchBox value={q} onChange={setQ} placeholder="Çalışan ara" /></div>
      {employees.isPending || grantsQuery.isPending ? <TableSkeleton /> : rows.length === 0 ? (
        <div className="card"><EmptyState icon="users" title="Eşleşen çalışan yok" /></div>
      ) : (
        <div className="card table-wrap">
          <table className="table">
            <thead><tr><th>Çalışan</th><th>Aktif erişim</th><th>Yakında sona eren</th><th /></tr></thead>
            <tbody>
              {rows.map((e) => {
                const gs = grantsOf(e.id);
                const soon = gs.filter((g) => g.expiresOn && daysUntil(g.expiresOn, timezone) <= 14).length;
                return (
                  <tr key={e.id} className="clickable" tabIndex={0} onClick={() => setOpenId(e.id)} onKeyDown={(k) => k.key === "Enter" && setOpenId(e.id)}>
                    <td><Person person={e} sub={e.jobTitle ?? false} /></td>
                    <td>{gs.filter((g) => g.status === "ACTIVE").length}</td>
                    <td>{soon ? <Badge tone="amber">{soon}</Badge> : <span className="muted">—</span>}</td>
                    <td className="actions" onClick={(k) => k.stopPropagation()}><Button size="sm" variant="ghost" iconRight="chevR" onClick={() => setOpenId(e.id)}>Ayrıntılar</Button></td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
      {open && <MemberDrawer employee={open} grants={grantsOf(open.id)} onClose={() => setOpenId(null)} />}
    </>
  );
}

function MemberDrawer({ employee, grants, onClose }: { employee: BasicEmployee; grants: Grant[]; onClose: () => void }) {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [revoke, setRevoke] = useState<Grant | null>(null);
  return (
    <Drawer open onClose={onClose} title={employee.fullName} sub={[employee.jobTitle, employee.department].filter(Boolean).join(" · ")}
      footer={<>
        <Button onClick={() => navigate(`/request-new?for=${employee.id}&pkg=1`)}>Başlangıç paketini talep et</Button>
        <Button variant="primary" icon="plus" onClick={() => navigate(`/request-new?for=${employee.id}`)}>Adına erişim talep et</Button>
      </>}>
      <div className="stack" style={{ gap: 22 }}>
        <div className="section-title">Erişimler ({grants.length})</div>
        {grants.length === 0 ? <p className="muted small">Açık erişim yok.</p> : grants.map((g) => (
          <div key={g.id} className="card" style={{ padding: 12 }}>
            <div className="between" style={{ alignItems: "flex-start" }}>
              <div className="stack-sm" style={{ gap: 6 }}>
                <ToolCell tool={g.tool} level={g.level} sub={<>Bitiş: <ExpiryCell grant={g} /></>} />
                <div className="row-wrap" style={{ gap: 6 }}><GrantBadges grant={g} />{g.retainedOnTransfer && <Tag warn>Transferde korundu</Tag>}{g.imported && <Tag>İçe aktarılan</Tag>}</div>
              </div>
              {removable(g) && <Button size="sm" variant="danger-ghost" onClick={() => setRevoke(g)}>Kaldır</Button>}
            </div>
          </div>
        ))}
      </div>
      <ConfirmDialog open={!!revoke} onClose={() => setRevoke(null)} title="Erişim kaldırılsın mı?"
        desc={revoke ? `${employee.fullName} kullanıcısının ${revoke.tool} · ${revoke.level} erişimi kaldırılır. Çalışan bilgilendirilir.` : undefined}
        confirmLabel="Kaldır" tone="danger" reason success="Kaldırma başlatıldı"
        onConfirm={async (reason) => { await api.post(`/grants/${revoke!.id}/revoke`, { version: revoke!.version, reason }); qc.invalidateQueries({ queryKey: ["access"] }); }} />
    </Drawer>
  );
}
