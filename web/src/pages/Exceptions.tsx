import { useQueryClient } from "@tanstack/react-query";
import { useState, type ReactNode } from "react";
import { useNavigate } from "react-router";
import { api } from "../api/client";
import { useEmployees, useExceptions, useTools } from "../api/queries";
import type { Approval, Operation, Orphan, Overlap } from "../api/types";
import { operationLabel, stepLabel } from "../i18n/labels";
import { useSession } from "../session";
import { Badge, Button, Callout, Card, ConfirmDialog, EmptyState, PageHeader, Person, Segmented, StatusBadge, TableSkeleton, ToolCell, useAction } from "../ui/components";
import { formatDateTime, relativeTime } from "../ui/format";
import { statusBadge } from "../ui/status";
import { DecisionDrawer } from "./Approvals";

type Group = "OPS" | "ACCOUNTS" | "FLOW";
type Exception = { key: string; title: string; high: boolean; group: Group; at: string | null; body: ReactNode; op?: Operation; approval?: Approval; orphan?: Orphan; finding?: boolean };

const opBody = (op: Operation, note?: string) => (
  <div className="stack-sm">
    <div className="row-wrap" style={{ gap: 18 }}>
      <Person person={op.employee} sub={false} />
      <ToolCell tool={op.tool} level={op.level} sub={operationLabel[op.type]} />
      <StatusBadge badge={statusBadge("operation", op.status)} />
    </div>
    {op.lastError && <div className="small" style={{ color: "var(--red)" }}>{op.lastError}{op.attempts ? ` · ${op.attempts} deneme` : ""}</div>}
    {note && <div className="small muted">{note}</div>}
  </div>
);

const overlapBody = (o: Overlap) => (
  <div className="stack-sm">
    <div className="row-wrap" style={{ gap: 18 }}><Person person={o.employee} sub={false} /><ToolCell tool={o.tool} level={`${o.oldLevel} → ${o.newLevel}`} /><StatusBadge badge={statusBadge("operation", o.status)} /></div>
    {o.lastError && <div className="small" style={{ color: "var(--red)" }}>{o.lastError}</div>}
    <div className="small muted">Yeni seviye etkin, ancak eski seviyenin kaldırılması doğrulanamadı. Çalışan şu an iki seviyeye de sahip sayılıyor.</div>
  </div>
);

const sourceText = (o: Orphan) => o.source === "MOCK" ? "Araç servisi okuması (API)" : `${o.source === "CSV" ? "CSV içe aktarımı" : "Excel çalışma kitabı"} · ${o.fileName}`;

/** Why an account matched no employee, as a hint for InfoSec: nothing is assigned or removed automatically. */
function orphanKind(account: string, companyDomains: Set<string>): { label: string; why: string } {
  const domain = account.includes("@") ? account.split("@").pop()!.toLowerCase() : null; // domains are ASCII: no Turkish ı
  if (!domain || companyDomains.size === 0) return { label: "Manuel inceleme gerekli", why: "Hesap adı bir çalışanın e-postasına benzemiyor; kime ait olduğunu araçta inceleyin." };
  if (companyDomains.has(domain)) return { label: "Çalışan kaydı olmayan şirket hesabı", why: "Şirket alan adında, ancak hiçbir çalışan kaydıyla eşleşmedi: kayıt dışı veya eski bir çalışana ait olabilir." };
  return { label: "Harici olabilir (tedarikçi veya ajans)", why: `${domain} hiçbir çalışanın alan adı değil. Doğrulanmış bir dış hesapsa gerekçesiyle kapatın, değilse kaldırın.` };
}

export function ExceptionsPage() {
  const { me, hasRole, timezone, adminGroups } = useSession();
  const tools = useTools().data ?? [];
  const directory = useEmployees();
  const companyDomains = new Set((directory.data ?? []).map((e) => e.email.split("@").pop()!.toLowerCase()));
  // The server lets a Super Admin or a member of the tool's admin group retry (never on one's own access).
  const canRetry = (o: Operation) => o.employee.id !== me.id && (hasRole("SUPER_ADMIN") || adminGroups.some((g) => g.id === tools.find((t) => t.name === o.tool)?.adminGroup.id));
  const qc = useQueryClient();
  const query = useExceptions();
  const [filter, setFilter] = useState<"ALL" | Group>("ALL");
  const [openStep, setOpenStep] = useState<Approval | null>(null);
  const [orphanAction, setOrphanAction] = useState<{ o: Orphan; action: "revoke" | "dismiss" } | null>(null);
  const navigate = useNavigate();
  const [run, busy] = useAction();
  const readOnly = !hasRole("SUPER_ADMIN", "INFOSEC");
  const x = query.data;

  // One card per entry; an operation in several lists (e.g. a failed security removal) shows once, under the most specific heading.
  const seen = new Set<string>();
  const fromOps = (ops: Operation[], title: string, high: boolean, group: Group, note?: string) =>
    ops.filter((o) => !seen.has(o.id) && seen.add(o.id)).map((o): Exception => ({ key: o.id, title, high, group, at: o.failedAt ?? o.createdAt, body: opBody(o, note), op: o }));
  const list: Exception[] = !x ? [] : [
    // A failed leaver removal stays under operation errors, where failures are looked for.
    ...fromOps(x.securityRevocations.filter((o) => o.status === "FAILED"), "Ayrılan çalışanın kaldırması başarısız", true, "OPS",
      "Çalışanın işten çıkışı yapıldı, ancak bu araçtaki hesabın kaldırılması başarısız oldu. Yeniden deneyin; araçta doğrulanana veya onaylanana kadar burada kalır."),
    ...fromOps(x.securityRevocations, "Ayrılan çalışanın kalan erişimi", true, "ACCOUNTS",
      "Çalışanın işten çıkışı yapıldı (kaydı geçmişte korunur), ancak bu araçtaki hesabı henüz kapanmadı. Kaldırma sürüyor; araçta doğrulanana veya onaylanana kadar burada kalır."),
    ...fromOps(x.suspensionGaps, "Askıya alma tamamlanmadı", true, "OPS", "Askıdaki çalışanın bu araçtaki erişimi henüz devre dışı bırakılamadı."),
    ...x.overlaps.filter((o) => !seen.has(o.operationId) && seen.add(o.operationId)).map((o): Exception => ({ key: o.operationId, title: "Seviye çakışması", high: true, group: "OPS", at: null, body: overlapBody(o) })),
    ...fromOps(x.staleGrants, "Transferden sonra etkinleşen erişim", true, "OPS", "Transfer sırasında kaldırılması istenen erişim sonradan etkinleşti; kaldırma doğrulanana kadar izlenir."),
    ...x.orphanAccounts.map((o): Exception => {
      const kind = directory.isSuccess ? orphanKind(o.account, companyDomains) : { label: "Sahipsiz hesap", why: "" };
      return {
        key: o.id, title: kind.label, high: true, group: "ACCOUNTS", at: o.importedAt, orphan: o,
        body: (
          <div className="stack-sm">
            <div className="row-wrap" style={{ gap: 18 }}>
              <ToolCell tool={o.tool} level={o.level} />
              <div className="small"><div className="mono">{o.account}</div><div className="muted">Kaynak: {sourceText(o)}</div></div>
            </div>
            <div className="small muted">Hiçbir çalışan kaydıyla eşleşmedi (e-posta ile, büyük/küçük harf duyarsız); otomatik olarak kimseye atanmaz. {kind.why}</div>
          </div>
        ),
      };
    }),
    ...fromOps(x.failedOperations, "Başarısız işlem", false, "OPS"),
    ...x.unroutableSteps.map((a): Exception => ({
      key: a.step.id, title: "Uygun onaylayıcı yok", high: false, group: "FLOW", at: a.step.openedAt, approval: a,
      body: <div className="row-wrap" style={{ gap: 18 }}><Person person={a.item.beneficiary} sub={false} /><ToolCell tool={a.item.tool} level={a.item.level} sub={`${stepLabel[a.step.type]} adımı`} /></div>,
    })),
    ...x.reviewFindings.map((i): Exception => ({
      key: i.id, title: "Açık inceleme bulgusu", high: false, group: "FLOW", at: i.findingAt, finding: true,
      body: <div className="row-wrap" style={{ gap: 18 }}><Person person={i.employee} sub={false} /><ToolCell tool={i.tool} level={i.level} sub={`İnceleyen: ${i.reviewerName ?? "Bilgi Güvenliği havuzu"} · 21 günde yanıtlanmadı`} /></div>,
    })),
  ];
  const count = (g: Group) => list.filter((e) => e.group === g).length;
  const shown = list.filter((e) => filter === "ALL" || e.group === filter);

  const retry = (op: Operation) => run(() => api.post(`/operations/${op.id}/retry`, { version: op.version }), { success: "İşlem yeniden başlatıldı" })
    .then((r) => { if (r.ok) qc.invalidateQueries({ queryKey: ["exceptions"] }); });

  return (
    <>
      <PageHeader title="İstisnalar" sub="Yalnızca gerçek sorunlar: başarısız işlemler, ayrılanların kalan erişimleri, sahipsiz ve şüpheli hesaplar, açık inceleme bulguları ve onaylayıcısı olmayan adımlar. Olağan bekleyen işler burada listelenmez." />
      {readOnly && <div style={{ marginBottom: 14 }}><Callout icon="eye">Denetçi olarak bu ekranı salt okunur görüntülüyorsunuz.</Callout></div>}
      <div className="toolbar">
        <Segmented<"ALL" | Group> label="İstisna türü" value={filter} onChange={setFilter}
          options={[{ value: "ALL", label: `Tümü (${list.length})` }, { value: "OPS", label: `İşlem hataları (${count("OPS")})` }, { value: "ACCOUNTS", label: `Sahipsiz ve Şüpheli Hesaplar (${count("ACCOUNTS")})` }, { value: "FLOW", label: `Onay (${count("FLOW")})` }]} />
      </div>
      {query.isPending ? <TableSkeleton /> : shown.length === 0 ? (
        <div className="card"><EmptyState icon="check" title="Çözülmesi gereken istisna yok" desc="Yeni bir sorun oluştuğunda burada görünür." /></div>
      ) : (
        <div className="stack-sm">
          {shown.map((e) => (
            <Card key={e.key}>
              <div className="between" style={{ alignItems: "flex-start", gap: 16, flexWrap: "wrap" }}>
                <div className="stack-sm" style={{ flex: 1, minWidth: 260, gap: 12 }}>
                  <div className="row-wrap" style={{ gap: 8 }}>
                    <span className="strong">{e.title}</span>
                    {e.high ? <Badge tone="red" dot>Yüksek</Badge> : <Badge tone="amber" dot>Orta</Badge>}
                    {e.at && <span className="faint small">{formatDateTime(e.at, timezone)} · {relativeTime(e.at)}</span>}
                  </div>
                  {e.body}
                </div>
                {!readOnly && e.op?.status === "FAILED" && canRetry(e.op) && <div className="row"><Button size="sm" icon="refresh" loading={busy} onClick={() => retry(e.op!)}>Yeniden dene</Button></div>}
                {!readOnly && e.approval && e.approval.item.beneficiary.id !== me.id && <div className="row"><Button size="sm" onClick={() => setOpenStep(e.approval!)}>{hasRole("SUPER_ADMIN") ? "Yeniden ata veya geçersiz kıl" : "Yeniden ata"}</Button></div>}
                {e.orphan && !hasRole("INFOSEC") && <div className="small muted" style={{ maxWidth: 220 }}>Bilgi Güvenliği ekibi hesabı kaldırır veya gerekçesiyle kapatır.</div>}
                {e.orphan && hasRole("INFOSEC") && (
                  <div className="row">
                    <Button size="sm" onClick={() => setOrphanAction({ o: e.orphan!, action: "dismiss" })}>Bulguyu kapat</Button>
                    <Button size="sm" variant="danger-ghost" onClick={() => setOrphanAction({ o: e.orphan!, action: "revoke" })}>Hesabı kaldır</Button>
                  </div>
                )}
                {e.finding && <div className="row"><Button size="sm" onClick={() => navigate("/reviews")}>İncelemeye git</Button></div>}
              </div>
            </Card>
          ))}
        </div>
      )}
      <ConfirmDialog open={!!orphanAction} onClose={() => setOrphanAction(null)} reason
        title={orphanAction?.action === "revoke" ? "Sahipsiz hesap kaldırılsın mı?" : "Bulgu kapatılsın mı?"}
        desc={orphanAction && (orphanAction.action === "revoke" ? `${orphanAction.o.account} hesabı ${orphanAction.o.tool} üzerinde normal iş akışıyla kaldırılır.`
          : "Hesap araçta olduğu gibi kalır. Doğrulanmış bir tedarikçi, ajans veya sistem hesabıysa nasıl doğruladığınızı gerekçeye yazın (ör. sözleşme veya talep numarası). Karar denetim kaydına işlenir.")}
        confirmLabel={orphanAction?.action === "revoke" ? "Hesabı kaldır" : "Bulguyu kapat"} tone={orphanAction?.action === "revoke" ? "danger" : "primary"}
        success={orphanAction?.action === "revoke" ? "Kaldırma başlatıldı" : "Bulgu kapatıldı"}
        onConfirm={async (reason) => { await api.post(`/orphans/${orphanAction!.o.id}/${orphanAction!.action}`, { reason, version: orphanAction!.o.version }); qc.invalidateQueries({ queryKey: ["exceptions"] }); }} />
      {openStep && <DecisionDrawer approval={openStep} canDecide={false} onClose={() => { setOpenStep(null); qc.invalidateQueries({ queryKey: ["exceptions"] }); }} />}
    </>
  );
}
