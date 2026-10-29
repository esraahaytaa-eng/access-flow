import { useState, type FormEvent, type ReactNode } from "react";
import { api } from "../api/client";
import { useDemo } from "../api/queries";
import type { Principal } from "../api/types";
import { errorMessage } from "../i18n/errors";
import { Badge, Icon, Logo } from "../ui/components";

/** The brand backdrop of the sign-in, forced password change and unreachable-server screens. */
export function AuthScreen({ children }: { children: ReactNode }) {
  return (
    <div className="login">
      <div className="login-bg" aria-hidden="true" />
      {children}
    </div>
  );
}

export function LoginPage({ onSignedIn }: { onSignedIn: (p: Principal) => void }) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [showPassword, setShowPassword] = useState(false);
  const [errors, setErrors] = useState<{ email?: string; password?: string }>({});
  const [failure, setFailure] = useState("");
  const [busy, setBusy] = useState(false);
  const demo = useDemo().data;

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const next = {
      email: !email.trim() ? "E-posta adresinizi girin." : !/^\S+@\S+\.\S+$/.test(email.trim()) ? "Geçerli bir e-posta adresi girin." : undefined,
      password: !password ? "Parolanızı girin." : undefined,
    };
    setErrors(next);
    setFailure("");
    if (next.email || next.password) return;
    setBusy(true);
    try {
      onSignedIn(await api.post<Principal>("/auth/login", { email: email.trim(), password }));
    } catch (err) {
      setFailure(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  // A demo account fills the e-mail only; its password is documented in the README.
  const pick = (address: string) => {
    setEmail(address);
    setErrors({});
    setFailure("");
    document.getElementById("login-password")?.focus();
  };

  return (
    <AuthScreen>
      <main className="login-box">
        <div className="login-card">
          <div className="login-logo" role="img" aria-label="AccessFlow"><Logo size={56} /></div>
          <h1>Giriş yap</h1>
          <form className="stack" onSubmit={submit} noValidate>
            <div className="field">
              <label className="label" htmlFor="login-email">E-posta</label>
              <div className="input-icon">
                <Icon name="mail" size={18} stroke={2} />
                <input id="login-email" className={`input ${errors.email ? "invalid" : ""}`} type="email" autoComplete="username" value={email}
                  aria-invalid={!!errors.email || undefined} onChange={(e) => setEmail(e.target.value)} placeholder="ad.soyad@sirket.com" />
              </div>
              {errors.email && <div className="error-text">{errors.email}</div>}
            </div>
            <div className="field">
              <label className="label" htmlFor="login-password">Parola</label>
              <div className="input-icon">
                <Icon name="lock" size={18} stroke={2} />
                <input id="login-password" className={`input ${errors.password ? "invalid" : ""}`} type={showPassword ? "text" : "password"} autoComplete="current-password"
                  value={password} aria-invalid={!!errors.password || undefined} onChange={(e) => setPassword(e.target.value)} placeholder="••••••••" />
                <button type="button" className="pw-toggle" aria-label={showPassword ? "Parolayı gizle" : "Parolayı göster"} onClick={() => setShowPassword(!showPassword)}>
                  <Icon name={showPassword ? "eyeOff" : "eye"} size={18} stroke={2} />
                </button>
              </div>
              {errors.password && <div className="error-text">{errors.password}</div>}
            </div>
            <button type="submit" className="btn btn-primary btn-lg btn-block login-submit" disabled={busy} aria-busy={busy || undefined}>
              {busy && <span className="spin" />}
              {busy ? "Doğrulanıyor…" : "Giriş yap"}
              {!busy && <Icon name="arrowR" size={18} stroke={2.2} />}
            </button>
            {failure && <div className="login-error" role="alert">{failure}</div>}
          </form>
        </div>
        {demo && (
          <section className="login-demo" aria-labelledby="login-demo-title">
            <div className="row" style={{ justifyContent: "space-between" }}>
              <h2 id="login-demo-title">Demo Accounts</h2>
              <Badge tone="amber" dot>Demo Environment</Badge>
            </div>
            <p className="muted small">Select an account. Passwords are listed in the README.</p>
            <ul role="list" aria-label="Demo Accounts">
              {demo.accounts.map((a) => (
                <li key={a.key}>
                  <button type="button" onClick={() => pick(a.email)}>
                    <span>{a.role}</span><span className="mono muted">{a.email}</span>
                  </button>
                </li>
              ))}
            </ul>
          </section>
        )}
        <p className="login-foot small">Yalnızca yetkili kullanıcılar içindir. İşlemler denetim kaydına yazılır.</p>
      </main>
    </AuthScreen>
  );
}
