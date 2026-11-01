// Request and grant cells shared by several pages (design/ spec 1.24).
import type { ApprovalStep, Duration, ExternalAccount, FulfillmentMethod, GitHubIntegration, Grant, RequestItem, RequestKind } from "../api/types";
import { methodLabel, methodTitle, requestKindLabel, stepLabel } from "../i18n/labels";
import { reasonText } from "../i18n/reasons";
import { useSession } from "../session";
import { Badge, Icon, StatusBadge, Tag } from "./components";
import { daysUntil, formatDate } from "./format";
import { displayBadges, statusBadge, suspensionBadge, type Badge as BadgeValue } from "./status";

/** Which service runs an API tool, from the backend's integration status (never the tool's name): the real
 *  GitHub adapter or the Mock Tool Service; null for a Manual or Vendor tool, or while the status is unknown. */
export function toolService(tool: { id: string; fulfillmentMethod: FulfillmentMethod }, status: GitHubIntegration | undefined): "GITHUB" | "MOCK" | null {
  if (tool.fulfillmentMethod !== "API" || !status) return null;
  return status.enabled && status.toolId === tool.id ? "GITHUB" : "MOCK";
}

const Badges = ({ badges }: { badges: BadgeValue[] }) => (
  <div className="row-wrap" style={{ gap: 6 }}>
    {badges.map((b, i) => i === 0 ? <StatusBadge key={i} badge={b} /> : <Badge key={i} tone={b.tone} title={b.label}><Icon name={b.tone === "green" ? "shieldCheck" : "user"} size={12} />{b.label}</Badge>)}
  </div>
);

export function GrantBadges({ grant }: { grant: Pick<Grant, "status" | "displayStatus" | "suspension"> }) {
  if (grant.suspension && (grant.status === "ACTIVE" || grant.status === "SUSPENDED")) return <StatusBadge badge={suspensionBadge(grant.suspension)} />;
  return <Badges badges={displayBadges(grant.displayStatus)} />;
}

export function ItemStateCell({ item, withSub = true }: { item: RequestItem; withSub?: boolean }) {
  if (item.status === "PENDING_APPROVAL") {
    const step = item.steps.find((s) => s.status === "PENDING");
    return (
      <div className="stack-sm" style={{ gap: 2 }}>
        <StatusBadge badge={statusBadge("item", item.status)} />
        {withSub && step && <span className="cell-sub">{stepLabel[step.type]} · {step.assigneeName ?? "Bilgi güvenliği havuzu"}</span>}
      </div>
    );
  }
  if (item.status === "APPROVED" && item.fulfillment) return <Badges badges={displayBadges(item.fulfillment.status)} />;
  return (
    <div className="stack-sm" style={{ gap: 2 }}>
      <StatusBadge badge={statusBadge("item", item.status)} />
      {withSub && item.closedReason && <span className="cell-sub">{reasonText(item.closedReason)}</span>}
    </div>
  );
}

const openStatuses = ["PROVISIONING", "PENDING_FULFILLMENT", "ACTIVE", "FAILED", "SUSPENDED", "REVOCATION_REQUESTED", "REVOCATION_FAILED"];
export const isOpenGrant = (g: Grant) => openStatuses.includes(g.status);

export function ExpiryCell({ grant }: { grant: Grant }) {
  const { timezone } = useSession();
  if (!grant.expiresOn) return <span className="muted">Kaldırılana kadar</span>;
  if (!isOpenGrant(grant)) return <span className="muted">{formatDate(grant.expiresOn, timezone)}</span>;
  const days = daysUntil(grant.expiresOn, timezone);
  return (
    <span>
      {formatDate(grant.expiresOn, timezone)}
      {days <= 14 && days >= 0 && <span style={{ marginLeft: 6 }}><Tag warn>{days === 0 ? "bugün" : `${days} gün kaldı`}</Tag></span>}
    </span>
  );
}

export function DurationText({ duration }: { duration: Duration }) {
  const { timezone } = useSession();
  return duration.type === "TEMPORARY" ? <>Süreli · {formatDate(duration.expiresOn, timezone)} tarihine kadar</> : <>Kaldırılana kadar</>;
}

export const RequestKindTag = ({ kind }: { kind: RequestKind }) => kind === "NEW_ACCESS" ? null : <Tag>{requestKindLabel[kind]}</Tag>;

/** A tool-specific account as approvers and auditors see it: the GitHub login and its immutable id. */
export const accountText = (a: ExternalAccount) => `@${a.name} · GitHub kimliği ${a.ref}`;

export const MethodTag = ({ method }: { method: FulfillmentMethod }) => <Tag title={methodTitle[method]}>{methodLabel[method]}</Tag>;

/** A level's approval steps. The server decides them one after another in this order: a stage opens only once the one before it is approved. */
export function ApprovalChain({ steps }: { steps: ApprovalStep["type"][] }) {
  return (
    <div className="chain">
      <span className="chain-count">{steps.length} aşamalı onay</span>
      <ol className="chain-steps" title="Onaylar sırayla alınır: bir aşama onaylanınca sıradaki açılır.">
        {steps.map((type, i) => (
          <li key={i} className="chain-step">
            {i > 0 && <Icon name="chevR" size={12} className="chain-arrow" />}
            <span className="chain-num">{i + 1}</span>{stepLabel[type]}
          </li>
        ))}
      </ol>
    </div>
  );
}
