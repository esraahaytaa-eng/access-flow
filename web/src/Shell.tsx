import { useQueryClient } from "@tanstack/react-query";
import { useRef, useState, type ReactNode } from "react";
import { NavLink, useLocation, useNavigate } from "react-router";
import { api } from "./api/client";
import { useAdminHealth, useDemo, useNotifications } from "./api/queries";
import type { Notification } from "./api/types";
import { roleLabel } from "./i18n/labels";
import { useNavCounts, useRoleFlags, type RoleFlags } from "./nav";
import { useSession } from "./session";
import { SuccessionPanel } from "./pages/Succession";
import { Avatar, Badge, Button, Callout, Icon, Logo, Modal, Popover, useAction } from "./ui/components";
import { formatDate, relativeTime } from "./ui/format";
import type { IconName } from "./ui/icons";

type NavItem = { to: string; label: string; icon: IconName; match?: string[]; count?: number; neutral?: boolean };

function navGroups(flags: RoleFlags, counts: ReturnType<typeof useNavCounts>) {
  const groups: { label: string | null; items: (NavItem | false)[] }[] = [
    { label: null, items: flags.auditor ? [] : [
      { to: "/", label: "Ana sayfa", icon: "home" },
      { to: "/catalog", label: "Araç kataloğu", icon: "grid", match: ["/catalog", "/request-new"] },
      { to: "/requests", label: "Taleplerim", icon: "list" },
      { to: "/access", label: "Erişimlerim", icon: "key" },
      { to: "/mailbox", label: "E-posta kutusu", icon: "mail" },
    ] },
    { label: "İş akışları", items: [
      flags.approvals && { to: "/approvals", label: "Onay kutusu", icon: "inbox", count: counts.approvals },
      flags.manager && { to: "/team", label: "Ekibim", icon: "users" },
      flags.reviews && { to: "/reviews", label: "Erişim Kontrolü", icon: "review", count: counts.reviews },
      flags.tasks && { to: "/tasks", label: "Karşılama görevleri", icon: "tasks", count: counts.tasks },
    ] },
    { label: "Yönetim", items: [
      flags.employees && { to: "/employees", label: "Çalışanlar", icon: "userCog" },
      flags.exceptions && { to: "/exceptions", label: "İstisnalar", icon: "alert", count: counts.exceptions, neutral: true },
      flags.admin && { to: "/tools-admin", label: "Araç yönetimi", icon: "box" },
      flags.admin && { to: "/direct", label: "Doğrudan erişim", icon: "zap" },
      flags.admin && { to: "/settings", label: "Yönetim ayarları", icon: "settings" },
    ] },
    { label: "Denetim", items: [
      flags.audit && { to: "/audit", label: "Denetim kayıtları", icon: "audit" },
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
    case "APPROVAL_POLICIES_IMPORTED": return `Excel içe aktarımıyla ${d.tools} araç ve ${d.levels} erişim seviyesinin onay adımları tanımlandı (${d.fileName}).`;
    case "DIRECT_GRANT": return `${d.tool} · ${d.level} için doğrudan erişim verildi (${formatDate(d.expiresOn as string, timeZone)} tarihine kadar).`;
    case "SUPER_ADMIN_OVERRIDE": return "Bir onay adımı Süper Yönetici tarafından geçersiz kılındı.";
    case "APPROVAL_ASSIGNED": return `Size bir onay adımı atandı${d.reason ? `: ${d.reason}` : "."}`;
    case "GRANTS_RETAINED_ON_TRANSFER": return `${d.employee} ekibinize transfer edildi; ${(d.grants as unknown[] | undefined)?.length ?? 0} erişimi korundu.`;
    case "REVIEW_STARTED": return d.toolOwnerChanged
      ? `${d.tool} aracının sahipliği size geçti; incelemeniz gereken ${d.items} denetim kalemi size yönlendirildi.`
      : `Erişim denetimi başladı; incelemeniz gereken ${d.items ?? ""} kalem olabilir.`;
    case "REVIEW_REMINDER": return "Yanıt bekleyen bir inceleme kaleminiz var (gün 7 hatırlatması).";
    case "REVIEW_ESCALATED": return "Yanıtlanmamış inceleme kalemleri Bilgi Güvenliğine eskale edildi.";
    case "SUPER_ADMIN_NOMINATED": return `${d.nominee} Süper Yönetici olarak önerildi; yetkilendirmeniz bekleniyor.`;
    case "SUPER_ADMIN_ROLE_OFFERED": return "İK'nın sizi yeni Süper Yönetici olarak belirlemesi yetkilendirildi. Kabul bağlantısı e-posta kutunuzda; 24 saat geçerlidir.";
    case "PRIVILEGED_ACTIVATION_LINK_VIEWED": return `${d.viewer} ayrıcalıklı hesap ${d.name} için etkinleştirme bağlantısını görüntüledi.`;
    case "SUPER_ADMIN_ROLE_ACCEPTED": return `${d.nominee} Süper Yönetici rolünü kabul etti; rol etkin.`;
    case "ADMIN_RECOVERY_REQUIRED": return "Aktif Süper Yönetici kalmadı; sistem kurtarma modunda.";
  }
}

const notificationLink: Partial<Record<Notification["kind"], string>> = {
  APPROVAL_ASSIGNED: "/approvals", GRANTS_RETAINED_ON_TRANSFER: "/team", REVIEW_STARTED: "/reviews", REVIEW_REMINDER: "/reviews", REVIEW_ESCALATED: "/reviews",
  SUPER_ADMIN_NOMINATED: "/approvals", SUPER_ADMIN_ROLE_OFFERED: "/mailbox",
};

function NotificationsMenu() {
  const { timezone } = useSession();
  const [open, setOpen] = useState(false);
  const qc = useQueryClient();
  const navigate = useNavigate();
  const list = useNotifications(!useSession().auditor).data ?? [];
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
  const { me, adminGroups, auditor } = useSession();
  const flags = useRoleFlags();
  if (auditor) return [roleLabel.AUDITOR];
  const labels = [flags.manager && "Yönetici", flags.owner && "Araç sahibi", ...me.roles.map((r) => roleLabel[r]), adminGroups.length > 0 && "Yönetim grubu üyesi"].filter(Boolean) as string[];
  return labels.length ? labels : ["Çalışan"];
}

function UserMenu({ onSignOut }: { onSignOut: () => void }) {
  const [open, setOpen] = useState(false);
  const [mfa, setMfa] = useState(false);
  const toggle = useRef<HTMLButtonElement>(null);
  // The menu item that opens the modal disappears with the menu, so the modal returns focus to the toggle.
  const openMfa = () => { setOpen(false); toggle.current?.focus(); setMfa(true); };
  const closeMfa = () => setMfa(false);
  const { me } = useSession();
  const navigate = useNavigate();
  const labels = useRoleLabels();
  return (
    <div style={{ position: "relative" }}>
      <button type="button" ref={toggle} className="btn btn-ghost" style={{ height: 40, paddingLeft: 6 }} onClick={() => setOpen(!open)} aria-haspopup="menu" aria-expanded={open}>
        <Avatar person={me} />
        <span style={{ textAlign: "left", lineHeight: 1.2 }} className="hide-sm">
          <span style={{ display: "block", fontWeight: 600 }}>{me.fullName}</span>
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
          {/* Planned, not implemented: nothing is enabled or stored (M18; see README). */}
          <button type="button" className="menu-item muted" onClick={openMfa}>
            <Icon name="key" /><span style={{ flex: 1 }}>Google Authenticator ile iki adımlı doğrulama</span><Badge tone="outline">Planlanıyor</Badge>
          </button>
          <button type="button" className="menu-item" onClick={() => { setOpen(false); onSignOut(); }}><Icon name="logout" />Çıkış yap</button>
        </div>
      </Popover>
      <Modal open={mfa} onClose={closeMfa} title="İki adımlı doğrulama" footer={<Button onClick={closeMfa}>Kapat</Button>}>
        <p>Bu özellik geliştirme aşamasındadır.</p>
      </Modal>
    </div>
  );
}

export function Shell({ children, onSignOut }: { children: ReactNode; onSignOut: () => void }) {
  const [navOpen, setNavOpen] = useState(false);
  const navigate = useNavigate();
  const demo = useDemo().data;
  const { auditor } = useSession();
  return (
    <div className="shell">
      <Sidebar open={navOpen} onClose={() => setNavOpen(false)} />
      {navOpen && <div className="overlay" style={{ zIndex: 25 }} onClick={() => setNavOpen(false)} />}
      <div className="main">
        <header className="topbar">
          <Button variant="ghost" icon="menu" className="menu-btn" aria-label="Menüyü aç" onClick={() => setNavOpen(true)} />
          {demo && <Badge tone="amber" dot title="Yerel demo: belgelenmiş demo hesapları ve örnek veriler">Demo Environment</Badge>}
          <div className="spacer" />
          {!auditor && <Button variant="primary" size="sm" icon="plus" className="topbar-request" aria-label="Erişim talep et" onClick={() => navigate("/request-new")}><span className="hide-xs">Erişim talep et</span></Button>}
          {!auditor && <NotificationsMenu />}
          <UserMenu onSignOut={onSignOut} />
        </header>
        <main className="content"><RecoveryBanner />{children}</main>
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

/** No active Super Admin (D2): HR, InfoSec and auditors are told; an HR Manager proposes a successor, InfoSec authorizes, the nominee accepts signed in as themself. */
export function RecoveryBanner() {
  const { hasRole, auditor } = useSession();
  const health = useAdminHealth(!auditor && hasRole("HR_MANAGER", "INFOSEC")).data;
  if (!health?.recoveryRequired) return null;
  return (
    <div style={{ marginBottom: 18 }}>
      <Callout tone="danger" title="Aktif Süper Yönetici yok: kurtarma modu">
        Erişim iş akışları çalışmaya devam eder. Bir İK Yöneticisi yeni Süper Yöneticiyi önerir, bağımsız bir Bilgi Güvenliği üyesi yetkilendirir; rol, aday kendi hesabıyla kabul edince etkinleşir.
        {hasRole("HR_MANAGER") && <SuccessionPanel />}
      </Callout>
    </div>
  );
}
