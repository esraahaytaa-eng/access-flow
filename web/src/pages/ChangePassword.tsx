import { useState, type FormEvent } from "react";
import { api, ApiError } from "../api/client";
import type { Principal } from "../api/types";
import { errorMessage } from "../i18n/errors";
import { Button, Callout, Card, Field, Input, Logo, PageHeader, useAction } from "../ui/components";

type Errors = { currentPassword?: string; newPassword?: string; repeat?: string };

// Not in the design (it has no password screen); built from its form and card primitives.
function PasswordForm({ onChanged }: { onChanged: (p: Principal) => void }) {
  const [form, setForm] = useState({ currentPassword: "", newPassword: "", repeat: "" });
  const [errors, setErrors] = useState<Errors>({});
  const [run, busy] = useAction();
  const set = (k: keyof typeof form) => (e: { target: { value: string } }) => { setForm({ ...form, [k]: e.target.value }); setErrors({ ...errors, [k]: undefined }); };

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const next: Errors = {
      currentPassword: form.currentPassword ? undefined : "Mevcut parolanızı girin.",
      newPassword: form.newPassword.length < 12 || form.newPassword.length > 128 ? "Parola 12–128 karakter olmalı." : undefined,
      repeat: form.repeat !== form.newPassword ? "Parolalar eşleşmiyor." : undefined,
    };
    setErrors(next);
    if (next.currentPassword || next.newPassword || next.repeat) return;
    const r = await run(() => api.post<Principal>("/auth/password", { currentPassword: form.currentPassword, newPassword: form.newPassword }), { success: "Parolanız değiştirildi" });
    if (r.ok) onChanged(r.res);
    else if (r.err instanceof ApiError && (r.err.field === "currentPassword" || r.err.field === "newPassword")) setErrors({ [r.err.field]: errorMessage(r.err) });
  };

  return (
    <form className="stack" onSubmit={submit} noValidate>
      <Field label="Mevcut parola" error={errors.currentPassword}>
        <Input type="password" autoComplete="current-password" value={form.currentPassword} invalid={!!errors.currentPassword} onChange={set("currentPassword")} />
      </Field>
      <Field label="Yeni parola" hint="12–128 karakter. Uzun bir parola cümlesi kullanabilirsiniz." error={errors.newPassword}>
        <Input type="password" autoComplete="new-password" value={form.newPassword} invalid={!!errors.newPassword} onChange={set("newPassword")} />
      </Field>
      <Field label="Yeni parola (tekrar)" error={errors.repeat}>
        <Input type="password" autoComplete="new-password" value={form.repeat} invalid={!!errors.repeat} onChange={set("repeat")} />
      </Field>
      <Button type="submit" variant="primary" loading={busy}>Parolayı değiştir</Button>
    </form>
  );
}

/** Shown instead of the app while the account has a temporary password. */
export function ForcedPasswordChange({ onChanged, onSignOut }: { onChanged: (p: Principal) => void; onSignOut: () => void }) {
  return (
    <div className="login">
      <div className="login-side">
        <div className="row" style={{ gap: 10 }}><Logo size={30} /><span style={{ fontWeight: 650, fontSize: 17, color: "#fff" }}>AccessFlow</span></div>
        <div><div className="big">Hesabınızı güvene alın.</div><p className="lead">Geçici parolanızı kendi belirlediğiniz bir parolayla değiştirin.</p></div>
        <div />
      </div>
      <div className="login-main">
        <div className="login-box">
          <h1 style={{ fontSize: 24, fontWeight: 640 }}>Parolanızı değiştirin</h1>
          <p className="muted" style={{ marginTop: 6, marginBottom: 24 }}>Devam etmeden önce geçici parolanızı değiştirmeniz gerekiyor.</p>
          <PasswordForm onChanged={onChanged} />
          <div style={{ marginTop: 16 }}><Button variant="ghost" block onClick={onSignOut}>Çıkış yap</Button></div>
        </div>
      </div>
    </div>
  );
}

export function ChangePasswordPage({ onChanged }: { onChanged: (p: Principal) => void }) {
  return (
    <>
      <PageHeader title="Parola değiştir" sub="Parolanız değişince diğer oturumlarınız kapanır." />
      <div style={{ maxWidth: 480 }} className="stack">
        <Callout>Yeni parolanız 12–128 karakter olmalı; harf, rakam veya sembol zorunluluğu yoktur.</Callout>
        <Card><PasswordForm onChanged={onChanged} /></Card>
      </div>
    </>
  );
}
