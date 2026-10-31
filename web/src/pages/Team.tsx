import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useNavigate } from "react-router";
import { api } from "../api/client";
import { useTeam, useTeamMemberGrants } from "../api/queries";
import type { Grant, TeamMember } from "../api/types";
import { Badge, Button, ConfirmDialog, Drawer, EmptyState, PageHeader, Pager, Person, SearchBox, TableSkeleton, Tag, ToolCell } from "../ui/components";
import { ExpiryCell, GrantBadges, isOpenGrant } from "../ui/domain";
const removable = (g: Grant) => ["ACTIVE", "FAILED", "PENDING_FULFILLMENT", "PROVISIONING", "SUSPENDED"].includes(g.status);

export function TeamPage() {
  const [q, setQText] = useState("");
  const [page, setPage] = useState(1);
  const [open, setOpen] = useState<TeamMember | null>(null);
  // Reports, their counts, search and pages come from the server, over the whole team (PERF-004).
  const team = useTeam({ q: q.trim(), page });
  const rows = team.data?.members ?? [];
  const setQ = (v: string) => { setQText(v); setPage(1); };

  return (
    <>
      <PageHeader title="Ekibim" sub="Doğrudan bağlı çalışanlarınızın erişimleri. İhtiyaç kalmayan erişimleri istediğiniz zaman kaldırabilir, ekibiniz adına erişim talep edebilirsiniz." />
      <div className="toolbar"><SearchBox value={q} onChange={setQ} placeholder="Çalışan ara" /></div>
      {team.isPending ? <TableSkeleton /> : rows.length === 0 ? (
        <div className="card"><EmptyState icon="users" title="Eşleşen çalışan yok" /></div>
      ) : (
        <div className="card table-wrap">
          <table className="table">
            <thead><tr><th>Çalışan</th><th>Aktif erişim</th><th>Yakında sona eren</th><th /></tr></thead>
            <tbody>
              {rows.map((e) => (
                <tr key={e.id} className="clickable" tabIndex={0} onClick={() => setOpen(e)} onKeyDown={(k) => k.key === "Enter" && setOpen(e)}>
                  <td><Person person={e} sub={e.jobTitle ?? false} /></td>
                  <td>{e.active}</td>
                  <td>{e.expiringSoon ? <Badge tone="amber">{e.expiringSoon}</Badge> : <span className="muted">—</span>}</td>
                  <td className="actions" onClick={(k) => k.stopPropagation()}><Button size="sm" variant="ghost" iconRight="chevR" onClick={() => setOpen(e)}>Ayrıntılar</Button></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {team.data && <Pager page={team.data.page} pageSize={team.data.pageSize} total={team.data.total} onChange={setPage} />}
      {open && <MemberDrawer employee={open} onClose={() => setOpen(null)} />}
    </>
  );
}

function MemberDrawer({ employee, onClose }: { employee: TeamMember; onClose: () => void }) {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const memberGrants = useTeamMemberGrants(employee.id);
  const grants = (memberGrants.data ?? []).filter(isOpenGrant);
  const [revoke, setRevoke] = useState<Grant | null>(null);
  return (
    <Drawer open onClose={onClose} title={employee.fullName} sub={[employee.jobTitle, employee.department].filter(Boolean).join(" · ")}
      footer={<Button variant="primary" icon="plus" onClick={() => navigate(`/request-new?for=${employee.id}`)}>Adına erişim talep et</Button>}>
      <div className="stack" style={{ gap: 22 }}>
        <div className="section-title">Erişimler ({grants.length})</div>
        {memberGrants.isPending ? <TableSkeleton rows={2} /> : grants.length === 0 ? <p className="muted small">Açık erişim yok.</p> : grants.map((g) => (
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
