import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link } from "react-router";
import { api } from "../api/client";
import { useTasks } from "../api/queries";
import type { FulfillmentTask, OperationType, TaskTab } from "../api/types";
import { methodLabel, operationLabel } from "../i18n/labels";
import { useSession } from "../session";
import { Badge, Button, Callout, ConfirmDialog, Drawer, EmptyState, Field, Input, KeyValueList, PageHeader, Pager, Person, SearchBox, Tabs, TableSkeleton, ToolCell, useAction } from "../ui/components";
import { formatDateTime, sinceText } from "../ui/format";
import type { Tone } from "../ui/status";

type Tab = TaskTab;
const kindTone: Record<OperationType, Tone> = { GRANT: "blue", REVOKE: "red", SUSPEND: "amber", RESUME: "green" };

/** PENDING is the queue; IN_PROGRESS is claimed, and a Vendor task with a ticket waits for the vendor. */
function taskState(t: FulfillmentTask): { label: string; tone: Tone } {
  if (t.status === "PENDING") return { label: "Sırada", tone: "outline" };
  if (t.method === "VENDOR" && t.ticketRef) return { label: "Tedarikçi bekleniyor", tone: "amber" };
  return { label: "Üstlenildi", tone: "blue" };
}

function instruction(t: FulfillmentTask) {
  const emp = t.employee.fullName;
  return {
    GRANT: `${emp} için ${t.tool} üzerinde “${t.level}” yetkisini tanımlayın.`,
    REVOKE: `${emp} kullanıcısının ${t.tool} üzerindeki “${t.level}” yetkisini kaldırın.`,
    SUSPEND: `${emp} kullanıcısının ${t.tool} hesabını devre dışı bırakın (silmeyin).`,
    RESUME: `${emp} kullanıcısının ${t.tool} hesabını yeniden etkinleştirin.`,
  }[t.type];
}

export function TasksPage() {
  const { adminGroups } = useSession();
  const [tab, setTabValue] = useState<Tab>("queue");
  const [q, setQText] = useState("");
  const [page, setPage] = useState(1);
  // The opened task is kept here: claiming moves it to another tab, the drawer stays.
  const [open, setOpen] = useState<FulfillmentTask | null>(null);
  // Tabs, search and pages are served by the server over the whole queue (PERF-004).
  const query = useTasks({ tab, q: q.trim(), page });
  const rows = query.data?.tasks ?? [];
  const counts = query.data?.counts;
  const setTab = (v: Tab) => { setTabValue(v); setPage(1); };
  const setQ = (v: string) => { setQText(v); setPage(1); };

  return (
    <>
      <PageHeader title="Karşılama görevleri" sub={`Manuel ve tedarikçi araçlarında yapılması gereken işlemler. Üyesi olduğunuz gruplar: ${adminGroups.map((g) => g.name).join(", ")}.`} />
      <Tabs value={tab} onChange={setTab} tabs={(["queue", "mine", "others"] as Tab[]).map((v) => ({ value: v, label: { queue: "Kuyruk", mine: "Üzerimdekiler", others: "Diğer üyelerde" }[v], count: counts?.[v] }))} />
      <div className="toolbar"><SearchBox value={q} onChange={setQ} placeholder="Çalışan veya araç ara" /></div>
      {query.isPending ? <TableSkeleton /> : rows.length === 0 ? (
        <div className="card"><EmptyState icon="tasks" title={tab === "queue" ? "Kuyrukta görev yok" : tab === "mine" ? "Üzerinizde görev yok" : "Bu görünümde görev yok"} desc={tab === "queue" ? "Onaylanan manuel veya tedarikçi erişimleri burada görev olarak belirir." : undefined} /></div>
      ) : (
        <div className="card table-wrap">
          <table className="table">
            <thead><tr><th>İşlem</th><th>Araç ve seviye</th><th>Çalışan</th><th>Grup</th><th>Bekleme</th><th>Durum</th></tr></thead>
            <tbody>
              {rows.map((t) => (
                <tr key={t.id} className="clickable" tabIndex={0} onClick={() => setOpen(t)} onKeyDown={(e) => e.key === "Enter" && setOpen(t)}>
                  <td><Badge tone={kindTone[t.type]}>{operationLabel[t.type]}</Badge></td>
                  <td><ToolCell tool={t.tool} level={t.level} sub={methodLabel[t.method]} /></td>
                  <td><Person person={t.employee} sub={false} /></td>
                  <td className="small">{t.group}</td>
                  <td className="small nowrap">{sinceText(t.createdAt)}</td>
                  <td>
                    <div className="stack-sm" style={{ gap: 2 }}>
                      <Badge tone={taskState(t).tone} dot>{taskState(t).label}</Badge>
                      {t.claimedBy && <span className="cell-sub">{t.claimedBy.fullName}</span>}
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {query.data && <Pager page={query.data.page} pageSize={query.data.pageSize} total={query.data.total} onChange={setPage} />}
      {open && <TaskDrawer task={open} onChange={setOpen} onClose={() => setOpen(null)} />}
    </>
  );
}

function TaskDrawer({ task, onChange, onClose }: { task: FulfillmentTask; onChange: (t: FulfillmentTask) => void; onClose: () => void }) {
  const { me, timezone } = useSession();
  const qc = useQueryClient();
  const [ticket, setTicket] = useState("");
  const [ticketError, setTicketError] = useState("");
  const [failOpen, setFailOpen] = useState(false);
  const [run, busy] = useAction();
  const mine = task.status === "IN_PROGRESS" && task.claimedBy?.id === me.id;
  const needsTicket = mine && task.method === "VENDOR" && !task.ticketRef;
  const state = taskState(task);
  // Each action returns the task with a new version, which the drawer keeps; the lists refetch.
  const act = async (path: string, body: object, success: string, successDesc?: string) => {
    const r = await run(() => api.post<FulfillmentTask>(`/fulfillment-tasks/${task.id}/${path}`, { version: task.version, ...body }), { success, successDesc });
    if (r.ok) {
      qc.invalidateQueries({ queryKey: ["tasks"] });
      if (r.res.status === "IN_PROGRESS") onChange(r.res); else onClose();
    }
  };
  const recordTicket = () => {
    if (ticket.trim().length < 3) { setTicketError("Tedarikçi talep numarasını girin."); return; }
    act("ticket", { ticketRef: ticket.trim() }, "Talep numarası kaydedildi", "Durum: Tedarikçi bekleniyor");
  };

  const footer = task.status === "PENDING"
    ? <><Button onClick={onClose}>Kapat</Button><Button variant="primary" disabled={!task.canAct} loading={busy} onClick={() => act("claim", {}, "Görev üstlenildi")}>Görevi üstlen</Button></>
    : !mine ? <Button onClick={onClose}>Kapat</Button>
    : <>
        <Button variant="danger-ghost" onClick={() => setFailOpen(true)}>Başarısız bildir</Button>
        <span style={{ flex: 1 }} />
        {needsTicket
          ? <Button variant="primary" loading={busy} onClick={recordTicket}>Talep numarasını kaydet</Button>
          : <Button variant="primary" icon="check" loading={busy} onClick={() => act("confirm", {}, "İşlem tamamlandı", "Sonuç beyana dayalı olarak kaydedildi.")}>{task.method === "VENDOR" ? "Tedarikçi onayını kaydet" : "Tamamlandı olarak onayla"}</Button>}
      </>;

  return (
    <Drawer open onClose={onClose} title={`${operationLabel[task.type]} · ${task.tool}`} sub={`${task.level} · ${methodLabel[task.method]}`} footer={footer}>
      <div className="stack" style={{ gap: 20 }}>
        {!task.canAct && task.status === "PENDING" && <Callout tone="warn" title="Bu görevi üstlenemezsiniz">Kendi erişiminizle veya başlattığınız bir doğrudan işlemle ilgili görevi başka bir yönetici tamamlamalı.</Callout>}
        <Callout title="Yapılacak işlem">{instruction(task)}</Callout>
        <div className="grid-2">
          <div><div className="label" style={{ marginBottom: 6 }}>Çalışan</div><Person person={task.employee} /></div>
          <div><div className="label" style={{ marginBottom: 6 }}>Durum</div><Badge tone={state.tone} dot>{state.label}</Badge></div>
        </div>
        <KeyValueList items={[
          ["Grup", task.group],
          ["Oluşturulma", `${formatDateTime(task.createdAt, timezone)} · ${sinceText(task.createdAt)} bekliyor`],
          task.claimedBy && ["Üstlenen", task.claimedBy.fullName],
          task.ticketRef ? ["Tedarikçi talep no", <span className="mono">{task.ticketRef}</span>] : null,
          ["Hesap", <span className="mono">{task.account}</span>],
          task.requestId ? ["İlgili talep", <Link to={`/requests/${task.requestId}`}>{task.requestId.slice(0, 8)}</Link>] : ["Kaynak", "İçe aktarılan erişim; onaylayan bilinmiyor"],
        ]} />
        {needsTicket && (
          <Field label="Tedarikçi talep numarası" required error={ticketError} hint="Tedarikçiye açtığınız talebin referansı. Tedarikçi işlemleri bu numara olmadan tamamlanamaz.">
            <Input value={ticket} invalid={!!ticketError} placeholder="Örn. LOGO-2026-10432" onChange={(e) => { setTicket(e.target.value); setTicketError(""); }} />
          </Field>
        )}
        {mine && !needsTicket && <Callout tone="warn" icon="user">Onayınız <strong>beyana dayalı</strong> kanıt olarak kaydedilir; bağımsız olarak doğrulanmış sayılmaz. Adınız ve zaman damgası denetim kaydına işlenir.</Callout>}
      </div>
      <ConfirmDialog open={failOpen} onClose={() => setFailOpen(false)} title="İşlem başarısız olarak bildirilsin mi?" desc="Görev kapanır ve İstisnalar ekranında yeniden denenebilir bir hata olarak görünür."
        confirmLabel="Başarısız bildir" tone="danger" reason reasonLabel="Ne oldu?" success="Hata bildirildi"
        onConfirm={async (reason) => {
          await api.post(`/fulfillment-tasks/${task.id}/fail`, { version: task.version, reason });
          qc.invalidateQueries({ queryKey: ["tasks"] });
          onClose();
        }} />
    </Drawer>
  );
}
