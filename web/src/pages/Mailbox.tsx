import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { api } from "../api/client";
import type { Email, EmailOutboxResponse } from "../api/types";
import { useSession } from "../session";
import { Badge, Button, Callout, EmptyState, Modal, PageHeader, TableSkeleton, useAction } from "../ui/components";
import { formatDateTime, relativeTime } from "../ui/format";

/** The local Mock Email Outbox as a page for everyone: each user sees the emails the server lets them see. */
export function MailboxPage() {
  return <><PageHeader title="E-posta kutusu" sub="Yerel demo için simüle edilen e-postalar. Gerçek e-posta gönderilmez." /><Mailbox /></>;
}

export function Mailbox() {
  const { timezone } = useSession();
  const query = useQuery({ queryKey: ["email-outbox"], queryFn: () => api.get<EmailOutboxResponse>("/email-outbox").then((r) => r.emails) });
  const [open, setOpen] = useState<Email | null>(null);
  const emails = query.data ?? [];
  return (
    <div className="stack">
      <Callout tone="info" title="Simülasyon e-posta kutusu">Gerçek e-posta gönderilmez; bu kutu yalnızca yerel demo içindir. Yönetici onayı gerektiren talep bildirimlerini Bilgi Güvenliği, Süper Yönetici ve Denetçi görür. Etkinleştirme bağlantılarını yalnızca Süper Yönetici, her görüntüleme denetim kaydına yazılarak görebilir. Süper Yönetici rolü kabul bağlantısını yalnızca alıcısı görür.</Callout>
      {query.isPending ? <TableSkeleton /> : emails.length === 0 ? <div className="card"><EmptyState icon="mail" title="E-posta yok" /></div> : (
        <div className="card">
          {emails.map((m) => (
            <button key={m.id} className="list-row clickable" style={{ width: "100%", border: 0, background: "none", textAlign: "left", borderBottom: "1px solid var(--line-2)" }} onClick={() => setOpen(m)}>
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ fontWeight: 600 }}>{m.subject}{m.kind !== "ACCESS_REQUEST" && <> <Badge tone={m.linkAvailable ? "blue" : "outline"}>{!m.linkAvailable ? "Bağlantı kullanılamaz" : m.kind === "ACTIVATION" ? "Etkinleştirme" : "Rol kabulü"}</Badge></>}</div>
                <div className="cell-sub" style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>Alıcı: {m.recipient.fullName} · {m.body}</div>
              </div>
              <span className="small muted nowrap">{relativeTime(m.createdAt)}</span>
            </button>
          ))}
        </div>
      )}
      <Modal open={!!open} onClose={() => setOpen(null)} title={open?.subject ?? ""} desc={open ? `Alıcı: ${open.recipient.fullName} · ${formatDateTime(open.createdAt, timezone)}` : undefined} footer={<Button onClick={() => setOpen(null)}>Kapat</Button>}>
        <p style={{ whiteSpace: "pre-wrap" }}>{open?.body}</p>
        {open && open.kind !== "ACCESS_REQUEST" && <EmailLink key={open.id} email={open} />}
      </Modal>
    </div>
  );
}

const linkKinds = {
  ACTIVATION: { path: "activation-link", page: "sifre-belirle", title: "Etkinleştirme bağlantısı (tek kullanımlık, 24 saat)", note: "Görüntülemeniz denetim kaydına yazıldı. Bağlantıyı yalnızca çalışana iletin.",
    gone: "Bu bağlantı artık kullanılamaz (kullanıldı, süresi doldu veya yenisi gönderildi). Gerekirse Hesap etkinleştirme sekmesinden yeni bağlantı gönderin." },
  ROLE_ACCEPTANCE: { path: "role-link", page: "rol-kabul", title: "Rol kabul bağlantısı (tek kullanımlık, 24 saat)", note: "Bağlantı yalnızca kendi hesabınızla oturum açmışken çalışır; parolanız değişmez.",
    gone: "Bu bağlantı artık kullanılamaz (kullanıldı, süresi doldu veya yenisi gönderildi). Gerekirse İK’dan yeni bağlantı isteyin." },
};

/** Reveals a link on request only: fetched into this component, never cached. */
function EmailLink({ email }: { email: Email }) {
  const [link, setLink] = useState("");
  const [run, busy] = useAction();
  const k = linkKinds[email.kind as keyof typeof linkKinds];
  if (!email.linkAvailable) return <Callout tone="warn">{k.gone}</Callout>;
  if (link) return (
    <Callout tone="warn" title={k.title}>
      <a href={link} target="_blank" rel="noreferrer noopener" style={{ wordBreak: "break-all" }}>{link}</a>
      <div className="small muted" style={{ marginTop: 6 }}>{k.note}</div>
    </Callout>
  );
  return <Button loading={busy} icon="key" onClick={() => run(() => api.get<{ token: string }>(`/email-outbox/${email.id}/${k.path}`))
    .then((r) => { if (r.ok) setLink(`${window.location.origin}/${k.page}#${r.res.token}`); })}>Bağlantıyı göster</Button>;
}
