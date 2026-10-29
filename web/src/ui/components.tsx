// Design primitives from design/ (markup and class names kept verbatim; styles in styles.css).
import {
  Children,
  cloneElement,
  createContext,
  Fragment,
  isValidElement,
  useCallback,
  useContext,
  useEffect,
  useId,
  useRef,
  useState,
  type ButtonHTMLAttributes,
  type CSSProperties,
  type InputHTMLAttributes,
  type ReactElement,
  type ReactNode,
  type SelectHTMLAttributes,
  type TextareaHTMLAttributes,
} from "react";
import { useQueryClient } from "@tanstack/react-query";
import { ApiError } from "../api/client";
import { icons, type IconName } from "./icons";
import type { Badge as BadgeValue, Tone } from "./status";
import { errorMessage } from "../i18n/errors";

const cx = (...c: (string | false | null | undefined)[]) => c.filter(Boolean).join(" ");

export function Icon({ name, size = 16, stroke = 1.8, className, style }: {
  name: IconName; size?: number; stroke?: number; className?: string; style?: CSSProperties;
}) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={stroke}
      strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" className={className} style={style}
      dangerouslySetInnerHTML={{ __html: icons[name] }} />
  );
}

export function Logo({ size = 28 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 32 32" aria-hidden="true">
      <rect width="32" height="32" rx="8" style={{ fill: "var(--brand)" }} />
      <path d="M9 21.5 14.2 10h3.6L23 21.5" fill="none" stroke="#fff" strokeWidth="2.4" strokeLinecap="round" strokeLinejoin="round" />
      <path d="M11.6 17h9.6" style={{ stroke: "var(--accent-soft)" }} strokeWidth="2.4" strokeLinecap="round" />
    </svg>
  );
}

type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: "default" | "primary" | "danger" | "danger-ghost" | "ghost";
  size?: "sm" | "lg";
  icon?: IconName;
  iconRight?: IconName;
  loading?: boolean;
  block?: boolean;
};

export function Button({ variant = "default", size, icon, iconRight, loading, block, className, children, disabled, ...props }: ButtonProps) {
  const cls = cx("btn", variant !== "default" && `btn-${variant}`, size && `btn-${size}`, !children && "btn-icon", block && "btn-block", className);
  return (
    <button type="button" className={cls} disabled={loading || disabled} aria-busy={loading || undefined} {...props}>
      {loading ? <span className="spin" /> : icon && <Icon name={icon} size={size === "sm" ? 14 : 16} />}
      {children}
      {iconRight && !loading && <Icon name={iconRight} size={14} />}
    </button>
  );
}

export function Badge({ tone, dot, children, title }: { tone?: Tone | ""; dot?: boolean; children: ReactNode; title?: string }) {
  return <span className={`badge ${tone || ""}`} title={title}>{dot && <span className="dot" />}{children}</span>;
}

export const StatusBadge = ({ badge }: { badge: BadgeValue }) => <Badge tone={badge.tone} dot>{badge.label}</Badge>;

// A file input with Turkish text (the browser's own reads "Choose File" in its language); the
// real input stays focusable and named for keyboard and screen-reader users.
export function FilePicker({ label, accept, fileName, onFile }: { label: string; accept: string; fileName?: string; onFile: (f: File | undefined) => void }) {
  return (
    <label className="file-pick">
      <input type="file" className="sr-only" accept={accept} aria-label={label} onChange={(e) => onFile(e.target.files?.[0])} />
      <span className="btn" aria-hidden="true"><Icon name="upload" size={16} />Dosya seç</span>
      <span className={fileName ? "small" : "muted small"}>{fileName || "Dosya seçilmedi"}</span>
    </label>
  );
}

export function Tag({ warn, children, title }: { warn?: boolean; children: ReactNode; title?: string }) {
  return <span className={`tag ${warn ? "warn" : ""}`} title={title}>{children}</span>;
}

// Muted tones of the olive palette, each dark enough for white initials (WCAG AA).
const avatarColors = ["#5f6e26", "#2f6b5e", "#3c5a80", "#6a4c7d", "#8a5a1f", "#4a5620", "#7a4636", "#36616f", "#5d5a2c", "#4d527f"];
const avatarColor = (id: string) => avatarColors[[...id].reduce((s, c) => s + c.charCodeAt(0), 0) % avatarColors.length];
const initials = (name: string) => name.split(" ").filter(Boolean).map((t) => t[0]).slice(0, 2).join("").toLocaleUpperCase("tr-TR");

type PersonLike = { id: string; fullName: string; status?: string };

export function Avatar({ person, size }: { person?: PersonLike | null; size?: "sm" | "lg" }) {
  const bg = !person || person.status === "TERMINATED" ? "var(--muted)" : avatarColor(person.id);
  return <span className={`avatar ${size || ""}`} style={{ background: bg }} aria-hidden="true">{person ? initials(person.fullName) : "?"}</span>;
}

export function Person({ person, sub, size }: { person?: PersonLike | null; sub?: ReactNode | false; size?: "sm" | "lg" }) {
  if (!person) return <span className="muted">—</span>;
  return (
    <div className="person">
      <Avatar person={person} size={size} />
      <div style={{ minWidth: 0 }}>
        <div className="name">{person.fullName}</div>
        {sub !== false && sub && <div className="meta">{sub}</div>}
      </div>
    </div>
  );
}

export function ToolMark({ name, size }: { name: string; size?: "sm" | "lg" }) {
  const words = name.split(" ").filter(Boolean);
  const label = words.length > 1 ? words[0][0] + words[1][0] : name.slice(0, 2);
  const c = avatarColor(name);
  return <span className={`toolmark ${size || ""}`} style={{ background: `${c}14`, color: c, boxShadow: `inset 0 0 0 1px ${c}22` }} aria-hidden="true">{label}</span>;
}

export function ToolCell({ tool, level, sub }: { tool: string; level?: string; sub?: ReactNode }) {
  return (
    <div className="toolcell">
      <ToolMark name={tool} size="sm" />
      <div style={{ minWidth: 0 }}>
        <div className="cell-main">{tool}{level && <span className="muted" style={{ fontWeight: 400 }}> · {level}</span>}</div>
        {sub && <div className="cell-sub">{sub}</div>}
      </div>
    </div>
  );
}

export function Card({ title, sub, actions, children, pad, className = "", style }: {
  title?: ReactNode; sub?: ReactNode; actions?: ReactNode; children: ReactNode; pad?: boolean; className?: string; style?: CSSProperties;
}) {
  return (
    <section className={`card ${className}`} style={style}>
      {(title || actions) && (
        <div className="card-head">
          <div><div className="card-title">{title}</div>{sub && <div className="card-sub">{sub}</div>}</div>
          {actions && <div className="row">{actions}</div>}
        </div>
      )}
      <div className={pad === false ? "" : "card-body"}>{children}</div>
    </section>
  );
}

export function PageHeader({ title, sub, actions, back }: {
  title: ReactNode; sub?: ReactNode; actions?: ReactNode; back?: { label: string; onClick: () => void };
}) {
  return (
    <>
      {back && <button type="button" className="crumb" onClick={back.onClick}><Icon name="chevL" size={14} />{back.label}</button>}
      <div className="page-head">
        <div><h1 className="page-title">{title}</h1>{sub && <p className="page-sub">{sub}</p>}</div>
        {actions && <div className="row-wrap">{actions}</div>}
      </div>
    </>
  );
}

export function EmptyState({ icon = "inbox", title, desc, action }: { icon?: IconName; title: ReactNode; desc?: ReactNode; action?: ReactNode }) {
  return (
    <div className="empty">
      <div className="empty-icon"><Icon name={icon} size={20} /></div>
      <div className="empty-title">{title}</div>
      {desc && <div className="empty-desc">{desc}</div>}
      {action && <div style={{ marginTop: 8 }}>{action}</div>}
    </div>
  );
}

export function Callout({ tone = "info", title, children, icon }: {
  tone?: "info" | "warn" | "danger" | "success"; title?: ReactNode; children?: ReactNode; icon?: IconName;
}) {
  const fallback: IconName = tone === "danger" || tone === "warn" ? "alert" : tone === "success" ? "check" : "info";
  return (
    <div className={`callout ${tone}`}>
      <Icon name={icon || fallback} size={16} />
      <div>{title && <div className="ct">{title}</div>}{children && <div>{children}</div>}</div>
    </div>
  );
}

export function Field({ label, hint, error, required, children, className = "" }: {
  label?: ReactNode; hint?: ReactNode; error?: string; required?: boolean; children: ReactElement<{ id?: string }>; className?: string;
}) {
  const generated = useId();
  const child = Children.only(children);
  const id = (isValidElement(child) && child.props.id) || generated;
  return (
    <div className={`field ${className}`}>
      {label && <label className="label" htmlFor={id}>{label}{required && <span className="req" aria-hidden="true">*</span>}</label>}
      {cloneElement(child as ReactElement<Record<string, unknown>>, { id, "aria-invalid": !!error || undefined })}
      {error ? <div className="error-text">{error}</div> : hint ? <div className="hint">{hint}</div> : null}
    </div>
  );
}

export const Input = ({ invalid, className = "", ...p }: InputHTMLAttributes<HTMLInputElement> & { invalid?: boolean }) =>
  <input className={`input ${invalid ? "invalid" : ""} ${className}`} {...p} />;

export const Textarea = ({ invalid, ...p }: TextareaHTMLAttributes<HTMLTextAreaElement> & { invalid?: boolean }) =>
  <textarea className={`textarea ${invalid ? "invalid" : ""}`} {...p} />;

export function Select({ invalid, options, placeholder, ...p }: SelectHTMLAttributes<HTMLSelectElement> & {
  invalid?: boolean; options: { value: string; label: string; disabled?: boolean }[]; placeholder?: string;
}) {
  return (
    <select className={`select ${invalid ? "invalid" : ""}`} {...p}>
      {placeholder !== undefined && <option value="">{placeholder}</option>}
      {options.map((o) => <option key={o.value} value={o.value} disabled={o.disabled}>{o.label}</option>)}
    </select>
  );
}

export function SearchBox({ value, onChange, placeholder = "Ara…", label = "Ara" }: {
  value: string; onChange: (v: string) => void; placeholder?: string; label?: string;
}) {
  return (
    <div className="search">
      <Icon name="search" size={15} />
      <input className="input" type="search" value={value} onChange={(e) => onChange(e.target.value)} placeholder={placeholder} aria-label={label} />
    </div>
  );
}

export function Tabs<T extends string>({ value, onChange, tabs }: {
  value: T; onChange: (v: T) => void; tabs: { value: T; label: string; count?: number }[];
}) {
  return (
    <div className="tabs" role="tablist">
      {tabs.map((t) => (
        <button type="button" key={t.value} role="tab" aria-selected={value === t.value} className={`tab ${value === t.value ? "on" : ""}`} onClick={() => onChange(t.value)}>
          {t.label}{t.count !== undefined && <span className="n">{t.count}</span>}
        </button>
      ))}
    </div>
  );
}

export function Switch({ checked, onChange, disabled, label }: { checked: boolean; onChange: (v: boolean) => void; disabled?: boolean; label: string }) {
  return (
    <span className="switch">
      <input type="checkbox" role="switch" aria-label={label} checked={checked} disabled={disabled} onChange={(e) => onChange(e.target.checked)} />
      <span />
    </span>
  );
}

export function KeyValueList({ items }: { items: ([ReactNode, ReactNode] | false | null | undefined)[] }) {
  return (
    <dl className="kv">
      {items.filter((i): i is [ReactNode, ReactNode] => !!i).map(([k, v], i) => <Fragment key={i}><dt>{k}</dt><dd>{v}</dd></Fragment>)}
    </dl>
  );
}

function useEscape(onEscape: () => void, active: boolean) {
  useEffect(() => {
    if (!active) return;
    const h = (e: KeyboardEvent) => e.key === "Escape" && onEscape();
    window.addEventListener("keydown", h);
    return () => window.removeEventListener("keydown", h);
  }, [onEscape, active]);
}

export function Modal({ open, onClose, title, desc, children, footer, wide }: {
  open: boolean; onClose: () => void; title: string; desc?: ReactNode; children: ReactNode; footer?: ReactNode; wide?: boolean;
}) {
  useEscape(onClose, open);
  if (!open) return null;
  return (
    <>
      <div className="overlay" onClick={onClose} />
      <div className="modal-wrap" onMouseDown={(e) => { if (e.target === e.currentTarget) onClose(); }}>
        <div className={`modal ${wide ? "wide" : ""}`} role="dialog" aria-modal="true" aria-label={title}>
          <div className="modal-head">
            <div><div className="modal-title">{title}</div>{desc && <div className="modal-desc">{desc}</div>}</div>
            <Button variant="ghost" size="sm" icon="x" aria-label="Kapat" onClick={onClose} />
          </div>
          <div className="modal-body">{children}</div>
          {footer && <div className="modal-foot">{footer}</div>}
        </div>
      </div>
    </>
  );
}

export function Drawer({ open, onClose, title, sub, children, footer }: {
  open: boolean; onClose: () => void; title: string; sub?: ReactNode; children: ReactNode; footer?: ReactNode;
}) {
  useEscape(onClose, open);
  if (!open) return null;
  return (
    <>
      <div className="overlay" onClick={onClose} />
      <aside className="drawer" role="dialog" aria-modal="true" aria-label={title}>
        <div className="drawer-head">
          <div style={{ minWidth: 0 }}><div className="modal-title">{title}</div>{sub && <div className="modal-desc">{sub}</div>}</div>
          <Button variant="ghost" size="sm" icon="x" aria-label="Kapat" onClick={onClose} />
        </div>
        <div className="drawer-body">{children}</div>
        {footer && <div className="drawer-foot">{footer}</div>}
      </aside>
    </>
  );
}

export function Popover({ open, onClose, children, style }: { open: boolean; onClose: () => void; children: ReactNode; style?: CSSProperties }) {
  const ref = useRef<HTMLDivElement>(null);
  useEscape(onClose, open);
  useEffect(() => {
    if (!open) return;
    const h = (e: MouseEvent) => { if (!ref.current?.parentElement?.contains(e.target as Node)) onClose(); };
    document.addEventListener("mousedown", h);
    return () => document.removeEventListener("mousedown", h);
  }, [open, onClose]);
  return open ? <div className="popover" ref={ref} style={style}>{children}</div> : null;
}

export function Skeleton({ h = 14, w = "100%", style }: { h?: number; w?: number | string; style?: CSSProperties }) {
  return <div className="skeleton" style={{ height: h, width: w, ...style }} />;
}

export function TableSkeleton({ rows = 5 }: { rows?: number }) {
  return (
    <div className="card" style={{ padding: 18 }} aria-busy="true" aria-label="Yükleniyor">
      <div className="stack-sm">
        {Array.from({ length: rows }, (_, i) => (
          <div key={i} className="row" style={{ gap: 14 }}>
            <Skeleton h={30} w={30} style={{ borderRadius: 8 }} /><Skeleton h={12} w="30%" /><Skeleton h={12} w="18%" /><Skeleton h={12} w="14%" />
          </div>
        ))}
      </div>
    </div>
  );
}

// ---- Toasts and the action runner -------------------------------------------------

type Toast = { id: number; type: "success" | "error" | "info"; title: string; desc?: string };
type ToastInput = Omit<Toast, "id" | "type"> & { type?: Toast["type"] };
const ToastContext = createContext<(t: ToastInput) => void>(() => {});
let toastSeq = 0;

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([]);
  const push = useCallback((t: ToastInput) => {
    const toast = { ...t, type: t.type ?? "success", id: ++toastSeq };
    setToasts((all) => [...all, toast]);
    setTimeout(() => setToasts((all) => all.filter((x) => x.id !== toast.id)), toast.type === "error" ? 6000 : 4200);
  }, []);
  return (
    <ToastContext.Provider value={push}>
      {children}
      <div className="toasts" role="status" aria-live="polite">
        {toasts.map((t) => (
          <div key={t.id} className={`toast ${t.type}`}>
            <span className="ti"><Icon name={t.type === "error" ? "alert" : t.type === "info" ? "info" : "check"} size={17} /></span>
            <div><div className="tt">{t.title}</div>{t.desc && <div className="td">{t.desc}</div>}</div>
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  );
}

export const useToast = () => useContext(ToastContext);

/** Runs one mutation at a time: busy while in flight, a toast on success or failure. */
export function useAction() {
  const toast = useToast();
  const qc = useQueryClient();
  const [busy, setBusy] = useState(false);
  const run = useCallback(async <T,>(fn: () => Promise<T>, opts: { success?: string; successDesc?: string } = {}) => {
    setBusy(true);
    try {
      const res = await fn();
      if (opts.success) toast({ type: "success", title: opts.success, desc: opts.successDesc });
      return { ok: true as const, res };
    } catch (err) {
      toast({ type: "error", title: "İşlem tamamlanamadı", desc: errorMessage(err) });
      // A conflict means the record changed meanwhile: reload, so a retry uses the current version and state.
      if (err instanceof ApiError && err.status === 409) qc.invalidateQueries();
      return { ok: false as const, err };
    } finally {
      setBusy(false);
    }
  }, [toast, qc]);
  return [run, busy] as const;
}

export function ConfirmDialog({
  open, onClose, title, desc, confirmLabel = "Onayla", tone = "primary", reason, reasonLabel = "Gerekçe",
  reasonPlaceholder, minReason = 5, onConfirm, children, success, successDesc,
}: {
  open: boolean; onClose: () => void; title: string; desc?: ReactNode; confirmLabel?: string; tone?: "primary" | "danger";
  reason?: boolean; reasonLabel?: string; reasonPlaceholder?: string; minReason?: number;
  onConfirm: (reason: string) => Promise<unknown>; children?: ReactNode; success?: string; successDesc?: string;
}) {
  const [text, setText] = useState("");
  const [error, setError] = useState("");
  const [run, busy] = useAction();
  useEffect(() => { if (open) { setText(""); setError(""); } }, [open]);
  const confirm = async () => {
    if (reason && text.trim().length < minReason) { setError(`${reasonLabel} en az ${minReason} karakter olmalı.`); return; }
    const r = await run(() => onConfirm(text.trim()), { success, successDesc });
    if (r.ok) onClose();
  };
  return (
    <Modal open={open} onClose={onClose} title={title} desc={desc} footer={<>
      <Button onClick={onClose}>Vazgeç</Button>
      <Button variant={tone} loading={busy} onClick={confirm}>{confirmLabel}</Button>
    </>}>
      <div className="stack">
        {children}
        {reason && (
          <Field label={reasonLabel} required error={error}>
            <Textarea value={text} onChange={(e) => { setText(e.target.value); setError(""); }} placeholder={reasonPlaceholder} rows={3} />
          </Field>
        )}
      </div>
    </Modal>
  );
}

export function Segmented<T extends string | number>({ value, onChange, options, label }: {
  value: T | null; onChange: (v: T) => void; options: { value: T; label: string }[]; label: string;
}) {
  return (
    <div className="seg" role="group" aria-label={label}>
      {options.map((o) => <button type="button" key={o.value} className={value === o.value ? "on" : ""} aria-pressed={value === o.value} onClick={() => onChange(o.value)}>{o.label}</button>)}
    </div>
  );
}
