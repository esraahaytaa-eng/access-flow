import { useEffect, useState, type FormEvent } from "react";
import { api, ApiError } from "../api/client";
import { errorMessage } from "../i18n/errors";
import { Button, Callout, Field, Input, Logo } from "../ui/components";
import { AuthScreen } from "./Login";

/**
 * The "Şifreni Belirle" page an activation link opens (M18). The token is after "#", so it never
 * reaches the server in the URL or a Referer; it is removed from the address bar at once and only
 * posted in the request body. Setting the password signs nobody in: the employee signs in next.
 */
export function SetPasswordPage() {
  const [token, setToken] = useState(() => window.location.hash.slice(1));
  const [form, setForm] = useState({ newPassword: "", repeat: "" });
  const [errors, setErrors] = useState<{ newPassword?: string; repeat?: string }>({});
  const [failure, setFailure] = useState("");
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState(false);
  useEffect(() => {
    // A new link opened in this tab differs only after "#", so the browser does not reload the page.
    const take = () => {
      const next = window.location.hash.slice(1);
      if (next) { setToken(next); setFailure(""); setDone(false); }
      window.history.replaceState(null, "", window.location.pathname);
    };
    take();
    window.addEventListener("hashchange", take);
    return () => window.removeEventListener("hashchange", take);
  }, []);
  const set = (k: keyof typeof form) => (e: { target: { value: string } }) => { setForm({ ...form, [k]: e.target.value }); setErrors({ ...errors, [k]: undefined }); };

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const next = {
      newPassword: [...form.newPassword].length < 12 || [...form.newPassword].length > 128 ? "Parola 12–128 karakter olmalı." : undefined,
      repeat: form.repeat !== form.newPassword ? "Parolalar eşleşmiyor." : undefined,
    };
    setErrors(next);
    setFailure("");
    if (next.newPassword || next.repeat) return;
    setBusy(true);
    try {
      await api.post("/auth/activate", { token, newPassword: form.newPassword });
      setDone(true);
    } catch (err) {
      if (err instanceof ApiError && err.field === "newPassword") setErrors({ newPassword: errorMessage(err) });
      else setFailure(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <AuthScreen>
      <main className="login-box">
        <div className="login-card">
          <div className="login-logo" role="img" aria-label="AccessFlow"><Logo size={56} /></div>
          <h1>Şifreni Belirle</h1>
          {done ? (
            <div className="stack">
              <Callout tone="success">Parolanız belirlendi. Şimdi e-posta adresiniz ve yeni parolanızla giriş yapın.</Callout>
              <a className="btn btn-primary btn-block" href="/">Giriş yap</a>
            </div>
          ) : !token ? (
            <div role="alert"><Callout tone="danger">{errorMessage(new ApiError(400, "invalid_activation_link"))}</Callout></div>
          ) : (
            <form className="stack" onSubmit={submit} noValidate>
              <p className="muted">AccessFlow hesabınız için bir parola belirleyin. Bağlantı yalnızca bir kez kullanılabilir.</p>
              {failure && <div role="alert"><Callout tone="danger">{failure}</Callout></div>}
              <Field label="Yeni parola" hint="12–128 karakter; yaygın parolalar ve e-posta adınız kabul edilmez." error={errors.newPassword}>
                <Input type="password" autoComplete="new-password" value={form.newPassword} invalid={!!errors.newPassword} onChange={set("newPassword")} />
              </Field>
              <Field label="Yeni parola (tekrar)" error={errors.repeat}>
                <Input type="password" autoComplete="new-password" value={form.repeat} invalid={!!errors.repeat} onChange={set("repeat")} />
              </Field>
              <Button type="submit" variant="primary" loading={busy}>Parolamı belirle</Button>
            </form>
          )}
        </div>
      </main>
    </AuthScreen>
  );
}
