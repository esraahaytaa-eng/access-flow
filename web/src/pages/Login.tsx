import { useState, type FormEvent } from "react";
import { api } from "../api/client";
import { useDemo } from "../api/queries";
import type { Principal } from "../api/types";
import { Badge, Button, Field, Input, Logo, useAction } from "../ui/components";

export function LoginPage({ onSignedIn }: { onSignedIn: (p: Principal) => void }) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [errors, setErrors] = useState<{ email?: string; password?: string }>({});
  const [run, busy] = useAction();
  const demo = useDemo().data;

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

  // A demo account fills the e-mail only; its password is documented in the README.
  const pick = (address: string) => {
    setEmail(address);
    setErrors({});
    document.getElementById("login-password")?.focus();
  };

  return (
    <div className="login">
      <main className="login-box">
        <div className="login-brand"><Logo size={30} /><span>AccessFlow</span></div>
        <div className="login-card">
          <h1>Oturum açın</h1>
          <p className="muted">Kurumsal e-posta adresiniz ve parolanızla devam edin.</p>
          <form className="stack" onSubmit={submit} noValidate>
            <Field label="E-posta" error={errors.email}>
              <Input type="email" autoComplete="username" value={email} invalid={!!errors.email} onChange={(e) => setEmail(e.target.value)} placeholder="ad.soyad@sirket.com.tr" />
            </Field>
            <Field label="Parola" error={errors.password}>
              <Input id="login-password" type="password" autoComplete="current-password" value={password} invalid={!!errors.password} onChange={(e) => setPassword(e.target.value)} />
            </Field>
            <Button type="submit" variant="primary" size="lg" block loading={busy}>Oturum aç</Button>
          </form>
        </div>
        {demo && (
          <section className="login-demo" aria-labelledby="login-demo-title">
            <div className="row" style={{ justifyContent: "space-between" }}>
              <h2 id="login-demo-title">Demo Accounts</h2>
              <Badge tone="amber" dot>Demo Environment</Badge>
            </div>
            <p className="faint small">Select an account. Passwords are listed in the README.</p>
            <ul role="list" aria-label="Demo Accounts">
              {demo.accounts.map((a) => (
                <li key={a.key}>
                  <button type="button" onClick={() => pick(a.email)}>
                    <span>{a.role}</span><span className="mono faint">{a.email}</span>
                  </button>
                </li>
              ))}
            </ul>
          </section>
        )}
        <p className="login-foot faint small">Yalnızca yetkili kullanıcılar içindir. İşlemler denetim kaydına yazılır.</p>
      </main>
    </div>
  );
}
