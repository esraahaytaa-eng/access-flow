import type { CSSProperties } from "react";
import { useNavigate } from "react-router";
import { useEmployees, useMyGrants, useRequests } from "../api/queries";
import type { Employee } from "../api/types";
import { useNavCounts, useRoleFlags } from "../nav";
import { useSession } from "../session";
import { Button, Callout, Card, EmptyState, Icon, ToolCell } from "../ui/components";
import { isOpenGrant, ItemStateCell, RequestKindTag } from "../ui/domain";
import { daysUntil, formatDate, relativeTime } from "../ui/format";
import type { IconName } from "../ui/icons";

function greeting(timeZone: string) {
  const hour = +new Intl.DateTimeFormat("en-GB", { hour: "2-digit", hourCycle: "h23", timeZone }).format(new Date());
  return hour < 12 ? "Günaydın" : hour < 18 ? "İyi günler" : "İyi akşamlar";
}

export function HomePage() {
  const navigate = useNavigate();
  const { me, timezone } = useSession();
  const flags = useRoleFlags();
  const counts = useNavCounts(flags);
  const employees = useEmployees().data ?? [];
  const requests = useRequests().data ?? [];
  const grants = useMyGrants().data ?? [];

  const directory = flags.employees ? (employees as Employee[]) : [];
  const tiles: { n: number; label: string; sub: string; to: string; icon: IconName }[] = [
    flags.approvals && { n: counts.approvals, label: "onay kararı bekliyor", sub: "Onay kutusu", to: "/approvals", icon: "inbox" as const },
    flags.tasks && { n: counts.tasks, label: "karşılama görevi açık", sub: "Karşılama görevleri", to: "/tasks", icon: "tasks" as const },
    flags.exceptions && { n: counts.exceptions, label: "istisna çözülmeyi bekliyor", sub: "İstisnalar", to: "/exceptions", icon: "alert" as const },
    flags.employees && { n: directory.filter((e) => e.termination || e.status === "SUSPENDED").length, label: "planlı çıkış veya askıdaki çalışan", sub: "Çalışanlar", to: "/employees", icon: "userCog" as const },
  ].filter((t) => !!t);
  const superAdmins = directory.filter((e) => e.status === "ACTIVE" && e.roles.includes("SUPER_ADMIN")).length;

  const recent = requests.flatMap((r) => r.items.map((it) => ({ it, kind: r.kind }))).sort((a, b) => b.it.createdAt.localeCompare(a.it.createdAt)).slice(0, 5);
  const open = grants.filter(isOpenGrant);
  const expiring = open.filter((g) => g.status === "ACTIVE" && g.expiresOn && daysUntil(g.expiresOn, timezone) <= 30).sort((a, b) => a.expiresOn!.localeCompare(b.expiresOn!));
  const today = new Intl.DateTimeFormat("tr-TR", { weekday: "long", day: "numeric", month: "long", timeZone: timezone }).format(new Date());

  return (
    <div className="stack" style={{ gap: 24 }}>
      <div className="page-head" style={{ marginBottom: 0 }}>
        <div><h1 className="page-title">{greeting(timezone)}, {me.fullName.split(" ")[0]}</h1><p className="page-sub">{today}</p></div>
        <div className="row-wrap">
          <Button icon="grid" onClick={() => navigate("/catalog")}>Kataloğa göz at</Button>
          <Button variant="primary" icon="plus" onClick={() => navigate("/request-new")}>Erişim talep et</Button>
        </div>
      </div>
      {flags.admin && directory.length > 0 && superAdmins < 2 && (
        <Callout tone="warn" title="Yalnızca bir aktif Süper Yönetici var">Süreklilik için ikinci bir Süper Yönetici gereklidir.</Callout>
      )}
      {tiles.length > 0 && (
        <div className="grid-3" style={tiles.length === 1 ? { gridTemplateColumns: "1fr" } : ({ "--cols": Math.min(tiles.length, 3) } as CSSProperties)}>
          {tiles.map((t) => (
            <button type="button" key={t.to} className="task-tile" onClick={() => navigate(t.to)}>
              <span className="tile-icon"><Icon name={t.icon} size={18} /></span><span className="num">{t.n}</span>
              <span style={{ flex: 1 }}><span style={{ display: "block", fontWeight: 600 }}>{t.label}</span><span className="muted small">{t.sub}</span></span>
              <Icon name="chevR" size={16} className="faint" />
            </button>
          ))}
        </div>
      )}
      <div className="grid-side">
        <Card title="Son taleplerim" pad={false} actions={<Button size="sm" variant="ghost" iconRight="chevR" onClick={() => navigate("/requests")}>Tümü</Button>}>
          {recent.length === 0
            ? <EmptyState icon="send" title="Henüz talebiniz yok" desc="İhtiyacınız olan aracı katalogda bulun ve birkaç adımda erişim talep edin." action={<Button variant="primary" onClick={() => navigate("/catalog")}>Kataloğu aç</Button>} />
            : recent.map(({ it, kind }) => (
              <div key={it.id} className="list-row clickable" role="link" tabIndex={0} onClick={() => navigate(`/requests/${it.requestId}`)} onKeyDown={(e) => e.key === "Enter" && navigate(`/requests/${it.requestId}`)}>
                <div style={{ flex: 1, minWidth: 0 }}>
                  <ToolCell tool={it.tool} level={it.level} sub={<>{it.beneficiary.id !== me.id ? `${it.beneficiary.fullName} adına · ` : ""}{relativeTime(it.createdAt)} <RequestKindTag kind={kind} /></>} />
                </div>
                <ItemStateCell item={it} />
              </div>
            ))}
        </Card>
        <div className="stack">
          <Card title="Erişimlerim" actions={<Button size="sm" variant="ghost" iconRight="chevR" onClick={() => navigate("/access")}>Tümü</Button>}>
            <div className="row" style={{ gap: 24 }}>
              <div><div style={{ fontSize: 26, fontWeight: 700 }}>{open.filter((g) => g.status === "ACTIVE").length}</div><div className="muted small">aktif erişim</div></div>
              <div><div style={{ fontSize: 26, fontWeight: 700 }}>{open.filter((g) => g.status !== "ACTIVE").length}</div><div className="muted small">işlemde</div></div>
            </div>
            {expiring.length > 0 && (
              <>
                <div className="divider" /><div className="section-title">Yakında sona erecek</div>
                <div className="stack-sm">{expiring.slice(0, 3).map((g) => <div key={g.id} className="between"><ToolCell tool={g.tool} level={g.level} /><span className="small muted nowrap">{formatDate(g.expiresOn, timezone)}</span></div>)}</div>
              </>
            )}
          </Card>
        </div>
      </div>
    </div>
  );
}
