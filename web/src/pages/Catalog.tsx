import { useState } from "react";
import { useNavigate } from "react-router";
import { useMyGrants, useTools } from "../api/queries";
import type { FulfillmentMethod } from "../api/types";
import { methodDesc } from "../i18n/labels";
import { Badge, Button, Drawer, EmptyState, PageHeader, Person, SearchBox, Select, Skeleton, Tag, ToolMark } from "../ui/components";
import { ApprovalChain, isOpenGrant, MethodTag } from "../ui/domain";

const lower = (s: string) => s.toLocaleLowerCase("tr-TR");

export function CatalogPage() {
  const navigate = useNavigate();
  const query = useTools();
  const grants = (useMyGrants().data ?? []).filter(isOpenGrant);
  const [q, setQ] = useState("");
  const [method, setMethod] = useState<"" | FulfillmentMethod>("");
  const [openId, setOpenId] = useState<string | null>(null);
  const tools = (query.data ?? [])
    .filter((t) => t.levels.some((l) => l.requestable))
    .filter((t) => !q.trim() || [t.name, ...t.levels.map((l) => l.name)].some((f) => lower(f).includes(lower(q.trim()))))
    .filter((t) => !method || t.fulfillmentMethod === method)
    .sort((a, b) => a.name.localeCompare(b.name, "tr"));
  const myGrant = (toolId: string) => grants.find((g) => g.toolId === toolId);
  const tool = tools.find((t) => t.id === openId);
  const mine = tool && myGrant(tool.id);

  return (
    <>
      <PageHeader title="Araç kataloğu" sub="Şirket içinde kullanılan araçlar ve erişim seviyeleri. Her seviyenin onay adımları önceden belirlidir." />
      <div className="toolbar">
        <SearchBox value={q} onChange={setQ} placeholder="Araç veya seviye ara" />
        <div style={{ width: 180 }}>
          <Select aria-label="Karşılama yöntemi" value={method} placeholder="Tüm yöntemler" onChange={(e) => setMethod(e.target.value as FulfillmentMethod)}
            options={[{ value: "API", label: "API" }, { value: "MANUAL", label: "Manuel" }, { value: "VENDOR", label: "Tedarikçi" }]} />
        </div>
        <span className="muted small" style={{ marginLeft: "auto" }}>{tools.length} araç</span>
      </div>
      {query.isPending ? (
        <div className="catalog">{Array.from({ length: 6 }, (_, i) => <div key={i} className="tool-card"><Skeleton h={34} w={34} /><Skeleton h={14} w="50%" /><Skeleton h={12} /><Skeleton h={12} w="70%" /></div>)}</div>
      ) : tools.length === 0 ? (
        <div className="card"><EmptyState icon="search" title="Sonuç bulunamadı" desc="Aramanızı veya filtreleri değiştirmeyi deneyin." action={<Button onClick={() => { setQ(""); setMethod(""); }}>Filtreleri temizle</Button>} /></div>
      ) : (
        <div className="catalog">
          {tools.map((t) => {
            const g = myGrant(t.id);
            return (
              <button type="button" key={t.id} className="tool-card" onClick={() => setOpenId(t.id)}>
                <div className="between" style={{ width: "100%" }}>
                  <div className="row"><ToolMark name={t.name} /><div><div className="strong">{t.name}</div><div className="muted small">{t.adminGroup.name}</div></div></div>
                  <MethodTag method={t.fulfillmentMethod} />
                </div>
                <div className="desc">{methodDesc[t.fulfillmentMethod]}</div>
                <div className="between" style={{ width: "100%" }}>
                  <span className="muted small">{t.levels.filter((l) => l.requestable).length} erişim seviyesi</span>
                  {g && <Badge tone={g.status === "ACTIVE" ? "green" : "amber"} dot>{g.level}{g.status !== "ACTIVE" ? " · işlemde" : ""}</Badge>}
                </div>
              </button>
            );
          })}
        </div>
      )}
      {tool && (
        <Drawer open onClose={() => setOpenId(null)} title={tool.name} sub={tool.adminGroup.name}
          footer={<><Button onClick={() => setOpenId(null)}>Kapat</Button><Button variant="primary" icon="plus" onClick={() => navigate(`/request-new?tool=${tool.id}`)}>Erişim talep et</Button></>}>
          <div className="stack" style={{ gap: 22 }}>
            <div className="grid-2">
              <div><div className="label" style={{ marginBottom: 6 }}>Araç sahibi</div><Person person={tool.owner} /></div>
              <div><div className="label" style={{ marginBottom: 6 }}>Karşılama</div><div className="row"><MethodTag method={tool.fulfillmentMethod} /></div><div className="hint" style={{ marginTop: 4 }}>{methodDesc[tool.fulfillmentMethod]}</div></div>
            </div>
            <div>
              <div className="section-title">Erişim seviyeleri</div>
              <div className="stack-sm">
                {tool.levels.filter((l) => l.requestable).map((l) => {
                  const isMine = mine?.accessLevelId === l.id;
                  return (
                    <div key={l.id} className="card" style={{ padding: 14 }}>
                      <div className="between" style={{ alignItems: "flex-start" }}>
                        <div style={{ minWidth: 0 }}>
                          <div className="row" style={{ gap: 8 }}><span className="strong">{l.name}</span>{isMine && <Tag>Sizde var</Tag>}{l.recommended && <Tag>Önerilen</Tag>}</div>
                          <div style={{ marginTop: 10 }}><ApprovalChain steps={l.steps.map((type) => ({ type, status: "WAITING" }))} /></div>
                        </div>
                        <Button size="sm" onClick={() => navigate(`/request-new?tool=${tool.id}&level=${l.id}`)}>{isMine ? "Süre uzat" : mine ? "Bu seviyeye geç" : "Talep et"}</Button>
                      </div>
                    </div>
                  );
                })}
              </div>
            </div>
            <p className="hint">Onaylar sırayla ilerler: önce yöneticiniz, gerekiyorsa araç sahibi, ardından Bilgi Güvenliği. Her adıma farklı bir kişi karar verir.</p>
          </div>
        </Drawer>
      )}
    </>
  );
}
