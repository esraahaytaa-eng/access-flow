import { useQueryClient } from "@tanstack/react-query";
import { useState, type ReactNode } from "react";
import { NavLink, useLocation, useNavigate } from "react-router";
import { api } from "./api/client";
import { useNotifications } from "./api/queries";
import type { Notification } from "./api/types";
import { roleLabel } from "./i18n/labels";
import { useNavCounts, useRoleFlags, type RoleFlags } from "./nav";
import { useSession } from "./session";
import { Avatar, Button, Icon, Logo, Popover, useAction } from "./ui/components";
import { formatDate, relativeTime } from "./ui/format";
import type { IconName } from "./ui/icons";

type NavItem = { to: string; label: string; icon: IconName; match?: string[]; count?: number; neutral?: boolean };

function navGroups(flags: RoleFlags, counts: ReturnType<typeof useNavCounts>) {
  const groups: { label: string | null; items: (NavItem | false)[] }[] = [
    { label: null, items: [
      { to: "/", label: "Ana sayfa", icon: "home" },
      { to: "/catalog", label: "Araç kataloğu", icon: "grid", match: ["/catalog", "/request-new"] },
      { to: "/requests", label: "Taleplerim", icon: "list" },
      { to: "/access", label: "Erişimlerim", icon: "key" },
    ] },
    { label: "İş akışları", items: [
      flags.approvals && { to: "/approvals", label: "Onay kutusu", icon: "inbox", count: counts.approvals },
      flags.manager && { to: "/team", label: "Ekibim", icon: "users" },
      flags.tasks && { to: "/tasks", label: "Karşılama görevleri", icon: "tasks", count: counts.tasks },
    ] },
    { label: "Yönetim", items: [
      flags.employees && { to: "/employees", label: "Çalışanlar", icon: "userCog" },
      flags.exceptions && { to: "/exceptions", label: "İstisnalar", icon: "alert", count: counts.exceptions, neutral: true },
      flags.admin && { to: "/tools-admin", label: "Araç yönetimi", icon: "box" },
      flags.admin && { to: "/direct", label: "Doğrudan erişim", icon: "zap" },
      flags.admin && { to: "/settings", label: "Yönetim ayarları", icon: "settings" },
    ] },
  ];
  return groups.map((g) => ({ ...g, items: g.items.filter((i): i is NavItem => !!i) })).filter((g) => g.items.length > 0);
}

/** "GMT+3" for the company timezone, as in the design's sidebar footer. */
const offsetName = (timeZone: string) =>
  new Intl.DateTimeFormat("en-US", { timeZone, timeZoneName: "shortOffset" }).formatToParts(new Date()).find((p) => p.type === "timeZoneName")?.value ?? "";

function Sidebar({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { timezone } = useSession();
  const flags = useRoleFlags();
  const counts = useNavCounts(flags);
  const { pathname } = useLocation();
  return (
    <aside className={`sidebar ${open ? "open" : ""}`} aria-label="Ana gezinme">
      <div className="brand"><Logo /><span className="brand-name">AccessFlow</span></div>
      <nav className="nav">
        {navGroups(flags, counts).map((g, i) => (
          <div key={i} className={g.label ? "nav-group" : ""}>
            {g.label && <div className="nav-label">{g.label}</div>}
            {g.items.map((it) => {
              const active = it.to === "/" ? pathname === "/" : (it.match ?? [it.to]).some((m) => pathname === m || pathname.startsWith(m + "/"));
              return (
                <NavLink key={it.to} to={it.to} className={`nav-item ${active ? "active" : ""}`} aria-current={active ? "page" : undefined} onClick={onClose}>
                  <Icon name={it.icon} size={17} /><span>{it.label}</span>
                  {!!it.count && <span className={`nav-count ${it.neutral ? "neutral" : ""}`}>{it.count}</span>}
                </NavLink>
              );
            })}
          </div>
        ))}
      </nav>
      <div className="sidebar-foot">Saatler: {timezone} ({offsetName(timezone)})</div>
    </aside>
  );
}

function notificationText(n: Notification, timeZone: string): string {
  const d = n.details as Record<string, unknown>;
  switch (n.kind) {
    case "APPROVAL_POLICY_CHANGED": return `${d.tool} · ${d.level} seviyesinin onay adımları değişti.`;
    case "DIRECT_GRANT": return `${d.tool} · ${d.level} için doğrudan erişim verildi (${formatDate(d.expiresOn as string, timeZone)} tarihine kadar).`;
    case "SUPER_ADMIN_OVERRIDE": return "Bir onay adımı Süper Yönetici tarafından geçersiz kılındı.";
    case "APPROVAL_ASSIGNED": return `Size bir onay adımı atandı${d.reason ? `: ${d.reason}` : "."}`;
    case "GRANTS_RETAINED_ON_TRANSFER": return `${d.employee} ekibinize transfer edildi; ${(d.grants as unknown[] | undefined)?.length ?? 0} erişimi korundu.`;
  }
}

const notificationLink: Partial<Record<Notification["kind"], string>> = { APPROVAL_ASSIGNED: "/approvals", GRANTS_RETAINED_ON_TRANSFER: "/team" };

function NotificationsMenu() {
  const { timezone } = useSession();
  const [open, setOpen] = useState(false);
  const qc = useQueryClient();
  const navigate = useNavigate();
  const list = useNotifications().data ?? [];
  const unread = list.filter((n) => !n.readAt);
  const [run] = useAction();
  const markRead = async (ns: Notification[]) => {
    await run(() => Promise.all(ns.map((n) => api.post(`/notifications/${n.id}/read`))));
    qc.invalidateQueries({ queryKey: ["notifications"] });
  };
  return (
    <div style={{ position: "relative" }}>
      <Button variant="ghost" icon="bell" aria-label={`Bildirimler${unread.length ? `, ${unread.length} okunmamış` : ""}`} onClick={() => setOpen(!open)} />
      {unread.length > 0 && <span style={{ position: "absolute", top: 6, right: 6, width: 8, height: 8, borderRadius: 9, background: "var(--red)", boxShadow: "0 0 0 2px var(--bg)" }} />}
      <Popover open={open} onClose={() => setOpen(false)}>
        <div className="between" style={{ padding: "12px 14px", borderBottom: "1px solid var(--line-2)" }}>
          <div className="strong">Bildirimler</div>
          {unread.length > 0 && <Button size="sm" variant="ghost" onClick={() => markRead(unread)}>Tümünü okundu say</Button>}
        </div>
        <div style={{ maxHeight: 380, overflowY: "auto", padding: 6 }}>
          {list.length === 0 && <div className="muted small" style={{ padding: 20, textAlign: "center" }}>Henüz bildiriminiz yok.</div>}
          {list.slice(0, 20).map((n) => (
            <button type="button" key={n.id} className="menu-item" style={{ alignItems: "flex-start" }} onClick={() => {
              setOpen(false);
              if (!n.readAt) markRead([n]);
              const to = notificationLink[n.kind];
              if (to) navigate(to);
            }}>
              <span style={{ width: 8, height: 8, borderRadius: 8, background: n.readAt ? "transparent" : "var(--accent)", marginTop: 7, flex: "none" }} />
              <span><span style={{ display: "block" }}>{notificationText(n, timezone)}</span><span className="faint small">{relativeTime(n.createdAt)}</span></span>
            </button>
          ))}
        </div>
      </Popover>
    </div>
  );
}

export function useRoleLabels() {
  const { me, adminGroups } = useSession();
  const flags = useRoleFlags();
  const labels = [flags.manager && "Yönetici", flags.owner && "Araç sahibi", ...me.roles.map((r) => roleLabel[r]), adminGroups.length > 0 && "Yönetim grubu üyesi"].filter(Boolean) as string[];
  return labels.length ? labels : ["Çalışan"];
}

function UserMenu({ onSignOut }: { onSignOut: () => void }) {
  const [open, setOpen] = useState(false);
  const { me } = useSession();
  const navigate = useNavigate();
  const labels = useRoleLabels();
  return (
    <div style={{ position: "relative" }}>
      <button type="button" className="btn btn-ghost" style={{ height: 40, paddingLeft: 6 }} onClick={() => setOpen(!open)} aria-haspopup="menu" aria-expanded={open}>
        <Avatar person={me} />
        <span style={{ textAlign: "left", lineHeight: 1.2 }} className="hide-sm">
          <span style={{ display: "block", fontWeight: 580 }}>{me.fullName}</span>
          <span className="muted" style={{ fontSize: 12 }}>{labels.slice(0, 2).join(" · ")}</span>
        </span>
        <Icon name="chevD" size={14} />
      </button>
      <Popover open={open} onClose={() => setOpen(false)} style={{ width: 320 }}>
        <div style={{ padding: "12px 14px", borderBottom: "1px solid var(--line-2)" }}>
          <div className="strong">{me.fullName}</div><div className="muted small">{me.email}</div>
          <div className="row-wrap" style={{ marginTop: 8, gap: 6 }}>{labels.map((r) => <span className="tag" key={r}>{r}</span>)}</div>
        </div>
        <div style={{ padding: 6 }}>
          <button type="button" className="menu-item" onClick={() => { setOpen(false); navigate("/password"); }}><Icon name="lock" />Parola değiştir</button>
          <button type="button" className="menu-item" onClick={() => { setOpen(false); onSignOut(); }}><Icon name="logout" />Çıkış yap</button>
        </div>
      </Popover>
    </div>
  );
}

export function Shell({ children, onSignOut }: { children: ReactNode; onSignOut: () => void }) {
  const [navOpen, setNavOpen] = useState(false);
  const navigate = useNavigate();
  return (
    <div className="shell">
      <Sidebar open={navOpen} onClose={() => setNavOpen(false)} />
      {navOpen && <div className="overlay" style={{ zIndex: 25 }} onClick={() => setNavOpen(false)} />}
      <div className="main">
        <header className="topbar">
          <Button variant="ghost" icon="menu" className="menu-btn" aria-label="Menüyü aç" onClick={() => setNavOpen(true)} />
          <div className="spacer" />
          <Button variant="primary" size="sm" icon="plus" onClick={() => navigate("/request-new")}>Erişim talep et</Button>
          <NotificationsMenu />
          <UserMenu onSignOut={onSignOut} />
        </header>
        <main className="content">{children}</main>
      </div>
    </div>
  );
}

const Blocked = ({ icon, title, desc }: { icon: IconName; title: string; desc: string }) => {
  const navigate = useNavigate();
  return (
    <div className="card" style={{ marginTop: 40 }}>
      <div className="empty">
        <div className="empty-icon"><Icon name={icon} size={20} /></div>
        <div className="empty-title">{title}</div>
        <div className="empty-desc">{desc}</div>
        <Button style={{ marginTop: 8 }} onClick={() => navigate("/")}>Ana sayfaya dön</Button>
      </div>
    </div>
  );
};

export const NoAccess = () => <Blocked icon="lock" title="Bu sayfaya erişiminiz yok" desc="Bu alan yalnızca ilgili role sahip kişilere açıktır." />;
export const NotFound = () => <Blocked icon="search" title="Sayfa bulunamadı" desc="Aradığınız sayfa taşınmış veya hiç var olmamış olabilir." />;
