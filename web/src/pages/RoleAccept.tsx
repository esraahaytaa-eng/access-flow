import { useState } from "react";
import { api } from "../api/client";
import { useSession } from "../session";
import { Button, Callout, Card, PageHeader, useAction } from "../ui/components";

// The token is read once and taken out of the address bar, so it is not kept in history or shown on screen.
const takeToken = () => {
  const token = window.location.hash.slice(1);
  if (token) window.history.replaceState(null, "", window.location.pathname);
  return token;
};

/** D2 succession: the nominee, signed in as themself, accepts the Super Admin role through their link.
 * Opening the link signs nobody in; the server checks the link belongs to this account. */
export function RoleAcceptPage() {
  const { me } = useSession();
  const [token] = useState(takeToken);
  const [done, setDone] = useState(false);
  const [run, busy] = useAction();
  const accept = () => run(() => api.post("/super-admin-nominations/accept", { token })).then((r) => setDone(r.ok));
  return (
    <>
      <PageHeader title="Süper Yönetici rolü" />
      <Card style={{ maxWidth: 640 }}>
        {done ? (
          <Callout tone="success" title="Süper Yönetici rolünüz etkinleşti">
            Güvenlik için oturumunuz kapatıldı. Yeni rolünüzle devam etmek için yeniden giriş yapın; parolanız değişmedi.
            <div style={{ marginTop: 10 }}><Button variant="primary" onClick={() => window.location.assign("/")}>Yeniden giriş yap</Button></div>
          </Callout>
        ) : !token ? (
          <Callout tone="warn">Bu sayfa yalnızca e-posta kutunuzdaki kabul bağlantısıyla açılır.</Callout>
        ) : (
          <div className="stack">
            <p>İK sizi AccessFlow’un yeni Süper Yöneticisi olarak belirledi. Kabul ettiğinizde rol <strong>{me.fullName}</strong> hesabında etkinleşir ve yeniden giriş yapmanız istenir.</p>
            <p className="hint">Bağlantı yalnızca bir kez ve 24 saat içinde kullanılabilir. Bu hesap size ait değilse rolü kabul etmeyin ve oturumu kapatın.</p>
            <div><Button variant="primary" loading={busy} onClick={accept}>Rolü kabul et</Button></div>
          </div>
        )}
      </Card>
    </>
  );
}
