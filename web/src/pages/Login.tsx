import { useState, type FormEvent } from "react";
import { api } from "../api/client";
import type { Principal } from "../api/types";
import { Button, Field, Icon, Input, Logo, useAction } from "../ui/components";

export function LoginPage({ onSignedIn }: { onSignedIn: (p: Principal) => void }) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [errors, setErrors] = useState<{ email?: string; password?: string }>({});
  const [run, busy] = useAction();

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const next = {
      email: !email.trim() ? "E-posta adresinizi girin." : !/^\S+@\S+\.\S+$/.test(email.trim()) ? "Geçerli bir e-posta adresi girin." : undefined,
      password: !password ? "Parolanızı girin." : undefined,
    };
    setErrors(next);
    if (next.email || next.password) return;
    const r = await run(() => api.post<Principal>("/auth/login", { email: email.trim(), password }));
    if (r.ok) onSignedIn(r.res);
  };

  return (
    <div className="login">
      <div className="login-side">
        <div className="row" style={{ gap: 10 }}><Logo size={30} /><span style={{ fontWeight: 650, fontSize: 17, color: "#fff" }}>AccessFlow</span></div>
        <div>
          <div className="big">Doğru kişiye, doğru erişim, doğru süre kadar.</div>
          <p className="lead">Erişim talepleri, onaylar, kaldırmalar ve çeyreklik incelemeler tek yerde. Her karar kayıt altında.</p>
          <div className="stack-sm" style={{ marginTop: 28, color: "#cfe0db", fontSize: 14 }}>
            {["Araca ve erişim seviyesine göre sıralı onaylar", "Ayrılan ve ekip değiştiren çalışanlarda otomatik kaldırma", "Denetime hazır, değiştirilemez kayıt geçmişi"].map((t) => (
              <div className="row" style={{ gap: 10 }} key={t}><Icon name="check" size={15} style={{ color: "#7fd6a8" }} />{t}</div>
            ))}
          </div>
        </div>
        <div />
      </div>
      <div className="login-main">
        <div className="login-box">
          <h1 style={{ fontSize: 24, fontWeight: 640 }}>Oturum açın</h1>
          <p className="muted" style={{ marginTop: 6, marginBottom: 24 }}>Kurumsal e-posta adresiniz ve parolanızla devam edin.</p>
          <form className="stack" onSubmit={submit} noValidate>
            <Field label="E-posta" error={errors.email}>
              <Input type="email" autoComplete="username" value={email} invalid={!!errors.email} onChange={(e) => setEmail(e.target.value)} placeholder="ad.soyad@accessflow.com.tr" />
            </Field>
            <Field label="Parola" error={errors.password}>
              <Input type="password" autoComplete="current-password" value={password} invalid={!!errors.password} onChange={(e) => setPassword(e.target.value)} />
            </Field>
            <Button type="submit" variant="primary" size="lg" block loading={busy}>Oturum aç</Button>
          </form>
        </div>
      </div>
    </div>
  );
}
