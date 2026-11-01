// Wire types of the AccessFlow JSON API (/api/v1), written by hand from the Go handlers.
// Conventions: ids are UUID strings; timestamps are RFC 3339 strings; fields named
// `expiresOn` are calendar dates "YYYY-MM-DD" in the company timezone; `| null` is nullable.

export type Role = "SUPER_ADMIN" | "INFOSEC" | "HR_MANAGER" | "AUDITOR";

/** A person reference embedded in other records. */
export type Person = { id: string; fullName: string };

export type AdminGroupRef = { id: string; name: string };

// ---- Auth -------------------------------------------------------------------------

/** Body of /auth/me, and of a successful /auth/login and /auth/password. */
export type Principal = {
  employee: { id: string; email: string; fullName: string; roles: Role[] };
  adminGroups: AdminGroupRef[];
  /** A temporary password: only /auth/me, /auth/logout and /auth/password work until changed. */
  mustChangePassword: boolean;
  /** IANA name, e.g. "Europe/Istanbul"; show times in it. */
  companyTimezone: string;
  /** A Super Admin nominee's role-acceptance link waits in their Mock Email Outbox (O2 note 2026-10-31). */
  roleOffer: boolean;
};

// ---- People -----------------------------------------------------------------------

export type EmployeeStatus = "ACTIVE" | "SUSPENDED" | "TERMINATED";

export type Termination = {
  at: string;
  reason: string;
  scheduledBy: string;
};

/** Full view: Super Admin, HR Manager, InfoSec and Auditor, and every mutation response. */
export type Employee = {
  id: string;
  email: string;
  fullName: string;
  department: string | null;
  jobTitle: string | null;
  managerId: string | null;
  status: EmployeeStatus;
  roles: Role[];
  adminGroups: AdminGroupRef[];
  mustChangePassword: boolean;
  /** False until the employee sets a password through an activation link (M18). */
  hasPassword: boolean;
  /** A scheduled termination, else null. */
  termination: Termination | null;
  version: number;
  createdAt: string;
};

/** What other employees see in GET /employees (active employees only). */
export type BasicEmployee = {
  id: string;
  email: string;
  fullName: string;
  department: string | null;
  jobTitle: string | null;
  managerId: string | null;
};

export type EmployeesResponse = { employees: Employee[] | BasicEmployee[] };

/** Create and rehire answer with the employee only; the activation link goes to the Mock Email Outbox. */
export type EmployeeResponse = { employee: Employee };

export type ActivationStatus = "PENDING" | "EXPIRED" | "COMPLETED" | "NO_LINK";
export type Activation = {
  employee: { id: string; fullName: string; email: string };
  status: ActivationStatus;
  reason: "CREATED" | "IMPORTED" | "REHIRED" | "RESET" | null;
  createdAt: string | null;
  expiresAt: string | null;
  completedAt: string | null;
};
export type ActivationsResponse = { activations: Activation[] };

export type AssignRoleBody = { role: "INFOSEC" | "HR_MANAGER" | "AUDITOR" };

/** `memberIds` is present only for Super Admin, HR Manager, InfoSec and Auditor. */
export type AdminGroup = { id: string; name: string; memberIds?: string[] };
export type AdminGroupsResponse = { groups: AdminGroup[] };

// ---- Catalog ----------------------------------------------------------------------

export type FulfillmentMethod = "API" | "MANUAL" | "VENDOR";
export type CatalogStatus = "ACTIVE" | "RETIRED";
export type StepType = "MANAGER" | "TOOL_OWNER" | "INFOSEC";

/** Catalog level. status/version only for Super Admin, InfoSec and Auditor. */
export type CatalogLevel = {
  id: string;
  name: string;
  steps: StepType[];
  status?: CatalogStatus;
  version?: number;
  requestable: boolean;
  /** In the viewer's starter package (a recommendation only). */
  recommended: boolean;
};

/** A tool's account identity when it is not the employee's email: a GitHub username. */
export type AccountKind = "GITHUB_LOGIN";

/** A tool-specific account a request named: GitHub's spelling of the login and its immutable user id. */
export type ExternalAccount = { name: string; ref: string };

/** Catalog tool. status/version only for Super Admin, InfoSec and Auditor. */
export type CatalogTool = {
  id: string;
  name: string;
  fulfillmentMethod: FulfillmentMethod;
  /** Absent: the account is the employee's email. */
  accountKind?: AccountKind;
  owner: Person;
  adminGroup: AdminGroupRef;
  status?: CatalogStatus;
  version?: number;
  levels: CatalogLevel[];
};

export type CatalogResponse = { tools: CatalogTool[] };

/** Administrative tool view returned by the tool mutations. */
export type Tool = {
  id: string;
  name: string;
  fulfillmentMethod: FulfillmentMethod;
  ownerId: string;
  adminGroupId: string;
  accountKind?: AccountKind;
  status: CatalogStatus;
  version: number;
};

export type StarterPackage = {
  id: string;
  department: string;
  /** null covers the whole department. */
  jobTitle: string | null;
  accessLevelId: string;
  tool: string;
  level: string;
  /** false once the level or its tool is retired. */
  requestable: boolean;
};

export type StarterPackagesResponse = { entries: StarterPackage[] };

// ---- Requests and approvals -------------------------------------------------------

export type RequestKind = "NEW_ACCESS" | "DIRECT_GRANT" | "EXTENSION" | "LEVEL_CHANGE";
export type ItemStatus = "PENDING_APPROVAL" | "APPROVED" | "REJECTED" | "WITHDRAWN" | "CANCELLED";
export type StepStatus =
  | "WAITING"
  | "PENDING"
  | "APPROVED"
  | "REJECTED"
  | "APPROVED_BY_SUBMISSION"
  | "SUPER_ADMIN_OVERRIDE"
  | "CANCELLED";

export type Duration =
  | { type: "UNTIL_REVOKED"; expiresOn: null }
  | { type: "TEMPORARY"; expiresOn: string };

export type ApprovalStep = {
  id: string;
  position: number;
  type: StepType;
  status: StepStatus;
  assigneeId: string | null;
  assigneeName: string | null;
  /** Present only while the step sits in the InfoSec pool. */
  pool?: "INFOSEC";
  /** Why it was routed there, e.g. "manager inactive → manager's manager". */
  routingNote: string | null;
  /** When the step opened; the inbox waiting time starts here. */
  openedAt: string | null;
  decidedBy: string | null;
  decidedByName: string | null;
  decidedAt: string | null;
  reason: string | null;
  version: number;
};

/** Grant display label: a status, or "<STATUS> / VERIFIED|SELF_REPORTED" for ACTIVE, SUSPENDED and REVOKED. */
export type DisplayStatus =
  | "PROVISIONING"
  | "PENDING_FULFILLMENT"
  | "WAITING_VENDOR"
  | "FAILED"
  | "REVOCATION_REQUESTED"
  | "REVOCATION_FAILED"
  | "CANCELLED"
  | `${"ACTIVE" | "SUSPENDED" | "REVOKED"} / ${"VERIFIED" | "SELF_REPORTED"}`;

export type Evidence = "VERIFIED" | "SELF_REPORTED";

/** The grant of an approved item (an EXTENSION shows the grant it extended). */
export type Fulfillment = {
  grantId: string;
  status: DisplayStatus;
  evidence: Evidence | null;
  closedReason: string | null;
};

export type RequestItem = {
  id: string;
  requestId: string;
  beneficiary: Person;
  submittedBy: Person;
  toolId: string;
  tool: string;
  accessLevelId: string;
  level: string;
  justification: string;
  duration: Duration;
  /** The tool-specific account the access is for; null when the tool uses the email. */
  account: ExternalAccount | null;
  status: ItemStatus;
  version: number;
  createdAt: string;
  closedAt: string | null;
  /** Set only on CANCELLED items, e.g. "access level retired", "transferred". */
  closedReason: string | null;
  /** [] for a direct grant. */
  steps: ApprovalStep[];
  fulfillment: Fulfillment | null;
};

export type Request = {
  id: string;
  submissionId: string;
  kind: RequestKind;
  beneficiary: Person;
  submittedBy: Person;
  createdAt: string;
  items: RequestItem[];
};

export type RequestsResponse = { requests: Request[] };

/** Open step with its item. `unroutable`: an InfoSec pool step no eligible member may decide. */
/** party: in Exceptions, why the caller may not reassign or override the unroutable step (separation of duties). */
export type Approval = { step: ApprovalStep; item: RequestItem; unroutable: boolean; party?: "beneficiary" | "submitter" | "approver" };
export type ApprovalsResponse = { approvals: Approval[] };

export type Email = {
  id: string;
  kind: "ACCESS_REQUEST" | "ACTIVATION" | "ROLE_ACCEPTANCE";
  recipient: Person;
  requestId: string | null;
  subject: string;
  body: string;
  createdAt: string;
  /** An activation link a Super Admin can still reveal (never listed itself). */
  linkAvailable: boolean;
};
export type EmailOutboxResponse = { emails: Email[] };

// ---- Access, operations, tasks ----------------------------------------------------

export type GrantStatus =
  | "PROVISIONING"
  | "PENDING_FULFILLMENT"
  | "ACTIVE"
  | "FAILED"
  | "SUSPENDED"
  | "REVOCATION_REQUESTED"
  | "REVOCATION_FAILED"
  | "REVOKED"
  | "CANCELLED";

export type Grant = {
  id: string;
  employee: Person;
  toolId: string;
  tool: string;
  accessLevelId: string;
  level: string;
  method: FulfillmentMethod;
  /** The tool-specific account (a GitHub login); null when the tool uses the email. */
  account: ExternalAccount | null;
  status: GrantStatus;
  displayStatus: DisplayStatus;
  evidence: Evidence | null;
  effectiveFrom: string | null;
  effectiveTo: string | null;
  expiresOn: string | null;
  /** Why it was revoked or cancelled. */
  closedReason: string | null;
  /** A SUSPEND or RESUME not yet verified or confirmed. */
  suspension: "SUSPENSION_REQUESTED" | "SUSPENSION_FAILED" | "RESUME_REQUESTED" | "RESUME_FAILED" | null;
  retainedOnTransfer: { at: string; justification: string } | null;
  /** Existed before AccessFlow; approver unknown (D10). */
  imported: boolean;
  /** When the tool read found this imported grant in the tool; null when only a spreadsheet reported it. */
  toolVerifiedAt: string | null;
  version: number;
  createdAt: string;
};

export type GrantsResponse = { grants: Grant[] };

/** One page of a server-paged list (PERF-004): `total` rows match, `pageSize` per page. */
export type Paged = { total: number; page: number; pageSize: number };

/** A direct report with their access counted on the server (GET /access/team). */
export type TeamMember = { id: string; fullName: string; jobTitle: string | null; department: string | null; active: number; expiringSoon: number };
export type TeamResponse = Paged & { members: TeamMember[] };

export type OperationType = "GRANT" | "REVOKE" | "SUSPEND" | "RESUME";
export type OperationStatus = "PENDING" | "IN_PROGRESS" | "SUCCEEDED" | "FAILED" | "CANCELLED";

export type Operation = {
  id: string;
  grantId: string;
  type: OperationType;
  employee: Person;
  tool: string;
  level: string;
  status: OperationStatus;
  attempts: number;
  lastError: string | null;
  /** Set while status is FAILED. */
  failedAt: string | null;
  version: number;
  createdAt: string;
};

/** A level change whose old level is still held because its removal failed. */
export type Overlap = {
  employee: Person;
  tool: string;
  oldGrantId: string;
  oldLevel: string;
  newGrantId: string;
  newLevel: string;
  operationId: string;
  status: OperationStatus;
  lastError: string | null;
};

export type Exceptions = {
  failedOperations: Operation[];
  unroutableSteps: Approval[];
  overlaps: Overlap[];
  suspensionGaps: Operation[];
  /** Open removals of terminated employees. */
  securityRevocations: Operation[];
  staleGrants: Operation[];
  /** Review items day 21 left unanswered (D8). */
  /** The first page of open findings; all of them are on the review page's findings view. */
  reviewFindings: ReviewItem[];
  reviewFindingsTotal: number;
  /** Imported accounts matching no employee (D10). */
  orphanAccounts: Orphan[];
  /** Every open record of each capped list; the lists hold the first (oldest or most urgent) ones. */
  totals: Record<CappedList, number>;
};

export type CappedList = "failedOperations" | "unroutableSteps" | "overlaps" | "suspensionGaps" | "securityRevocations" | "staleGrants" | "orphanAccounts";

/** A Manual or Vendor operation in its admin group's queue. */
export type FulfillmentTask = {
  id: string;
  grantId: string;
  type: OperationType;
  status: OperationStatus;
  method: "MANUAL" | "VENDOR";
  groupId: string;
  group: string;
  toolId: string;
  tool: string;
  accessLevelId: string;
  level: string;
  /** An orphan account's task names the account in place of a person (id is the nil UUID). */
  employee: Person;
  /** The account to act on in the tool. */
  account: string;
  /** null for an imported grant. */
  requestId: string | null;
  itemId: string | null;
  ticketRef: string | null;
  claimedBy: Person | null;
  /** false on the viewer's own grant or a direct action they initiated. */
  canAct: boolean;
  version: number;
  createdAt: string;
};

export type TaskTab = "queue" | "mine" | "others";
export type FulfillmentTasksResponse = Paged & { tasks: FulfillmentTask[]; counts: Record<TaskTab | "canAct", number> };

// ---- Notifications ----------------------------------------------------------------

export type NotificationKind =
  | "APPROVAL_POLICY_CHANGED"
  | "APPROVAL_POLICIES_IMPORTED"
  | "DIRECT_GRANT"
  | "SUPER_ADMIN_OVERRIDE"
  | "APPROVAL_ASSIGNED"
  | "GRANTS_RETAINED_ON_TRANSFER"
  | "REVIEW_STARTED"
  | "REVIEW_REMINDER"
  | "REVIEW_ESCALATED"
  | "SUPER_ADMIN_NOMINATED"
  | "SUPER_ADMIN_ROLE_OFFERED"
  | "SUPER_ADMIN_ROLE_ACCEPTED"
  | "PRIVILEGED_ACTIVATION_LINK_VIEWED"
  | "ADMIN_RECOVERY_REQUIRED";

export type Notification = {
  id: string;
  kind: NotificationKind;
  /** "access_level" | "request_item" | "approval_step" | "employee" */
  entity: string;
  entityId: string | null;
  /** Kind-specific; see API.md. */
  details: Record<string, unknown>;
  createdAt: string;
  readAt: string | null;
};

export type NotificationsResponse = { notifications: Notification[] };

// ---- Access reviews (D8) -------------------------------------------------------------

export type ReviewFlag = "RETAINED_ON_TRANSFER" | "EARLIER_POLICY" | "DIRECT_GRANT" | "OVERRIDE" | "SUSPENDED" | "IMPORTED_BASELINE";

export type ReviewCampaign = {
  id: string;
  kind: "QUARTERLY" | "MANUAL";
  quarter: string | null;
  startedAt: string;
  /** null: started by the quarterly schedule. */
  startedBy: Person | null;
  remindedAt: string | null;
  escalatedAt: string | null;
  closedAt: string | null;
};

export type ReviewItem = {
  id: string;
  campaignId: string;
  campaignStartedAt: string;
  grantId: string;
  employee: { id: string; fullName: string; jobTitle: string | null; status: EmployeeStatus };
  toolId: string;
  tool: string;
  accessLevelId: string;
  level: string;
  grantStatus: GrantStatus;
  expiresOn: string | null;
  effectiveFrom: string | null;
  /** null: the InfoSec pool. */
  reviewerId: string | null;
  reviewerName: string | null;
  routingNote: string | null;
  requiresInfosec: boolean;
  flags: ReviewFlag[];
  decision: "KEEP" | "REVOKE" | null;
  decidedBy: Person | null;
  decidedAt: string | null;
  decisionReason: string | null;
  late: boolean;
  findingAt: string | null;
  closedReason: "REVOKED_NO_REVIEW" | "ALREADY_REMOVED" | null;
  closedAt: string | null;
  version: number;
};

export type ReviewView = "mine" | "esc" | "all" | "findings";
/** One page of one view; counts are over every item the caller sees (PERF-004). */
export type ReviewPage = Paged & { campaign: ReviewCampaign | null; items: ReviewItem[]; view: ReviewView;
  counts: Record<ReviewView | "done" | "waiting", number> };

// ---- Baseline import and orphan accounts (D10) ------------------------------------------

export type ImportRejection = { line?: number; account: string; tool: string; level: string; reason: "unknown_tool" | "unknown_level" | "api_tool" | "invalid_row" };

export type ImportReport = {
  id: string;
  source: "MOCK" | "CSV" | "WORKBOOK";
  fileName: string | null;
  importedBy: Person;
  importedAt: string;
  imported: number;
  orphans: number;
  terminated: number;
  skipped: number;
  rejected: ImportRejection[];
  verified?: number; // tool read: reported grants it found in the tool
  unverified?: number; // tool read: reported API grants the tool does not hold
  created?: { employees: number; tools: number; levels: number }; // workbook
};

export type ImportsResponse = { imports: ImportReport[] };

export type Orphan = {
  id: string;
  toolId: string;
  tool: string;
  accessLevelId: string;
  level: string;
  method: FulfillmentMethod;
  account: string;
  status: GrantStatus;
  source: "MOCK" | "CSV" | "WORKBOOK";
  fileName: string | null;
  importedAt: string;
  version: number;
};

// Audit Explorer (M13). Rows are in the caller's audit scope (O7).
export type AuditEvent = {
  id: string;
  at: string;
  actor: Person | null; // null: the system
  action: string;
  entity: string;
  entityId: string | null;
  subject: Person | null;
  reason: string | null;
  details: Record<string, unknown> | null;
};
export type AuditEventsResponse = { events: AuditEvent[]; total: number; page: number; pageSize: number };
export type AuditFilters = { actors: Person[]; tools: { id: string; name: string }[] };

export type HeldState = "ACTIVE" | "SUSPENDED" | "REVOCATION_REQUESTED" | "REVOCATION_FAILED";
export type HeldGrant = {
  grantId: string;
  employee: Person | null; // null: an orphan account
  account: string | null;
  tool: string;
  level: string;
  method: FulfillmentMethod;
  state?: HeldState;
  evidence: Evidence | "IMPORTED";
  confirmedBy: Person | null;
  effectiveFrom: string;
  effectiveTo: string | null;
  removalEvidence: Evidence | null;
  removedBy: Person | null;
  imported: boolean;
  direct: Person | null;
  approvers: { type: StepType; status: string; name: string }[];
};
export type AccessAtResponse = { at: string; grants: HeldGrant[]; removed: HeldGrant[] };

export type PrivilegedHolder = { role: Role | "ADMIN_GROUP"; group: string | null; employee: Person; since: string; grantedBy: Person | null };
export type LeaverRow = {
  employee: Person; terminatedAt: string | null; tool: string; level: string; status: GrantStatus; effectiveTo: string | null; removalEvidence: Evidence | null;
};
export type QuarterReport = {
  campaign: { id: string; kind: "QUARTERLY" | "MANUAL"; quarter: string | null; startedAt: string; remindedAt: string | null; escalatedAt: string | null; closedAt: string | null } | null;
  counts: { total: number; kept: number; revoked: number; unanswered: number; alreadyRemoved: number; autoRevoked: number; openFindings: number; carriedFindings: number };
  policyChanges: number;
  overrides: number;
  items: { employee: string; tool: string; level: string; reviewer: string | null; decision: "KEEP" | "REVOKE" | null; decidedBy: string | null;
    decidedAt: string | null; reason: string | null; late: boolean; closedReason: string | null; finding: boolean;
    /** An open finding from an earlier campaign; not counted in total. */ carriedOver: boolean }[];
};

/** GET /super-admin-nominations: open, unexpired Super Admin nominations (D2). */
export type SuperAdminNomination = {
  id: string;
  nominee: { id: string; fullName: string };
  proposedBy: { id: string; fullName: string };
  /** Made while no Super Admin was active. */
  recovery: boolean;
  /** HR's succession of the last Super Admin: authorized by InfoSec or an active Super Admin, then the nominee accepts through a 24-hour link (D2, R-2). */
  succession: boolean;
  /** When a succession was authorized; its link exists only from then. */
  authorizedAt?: string | null;
  status: "PENDING" | "AUTHORIZED" | "DECLINED" | "ACCEPTED" | "EXPIRED" | "CANCELLED";
  createdAt: string;
  expiresAt: string;
  version: number;
  /** Whether the caller may authorize or decline it; the server decides. */
  canAuthorize: boolean;
};
export type NominationsResponse = { nominations: SuperAdminNomination[] };

/** GET /admin/health */
/** The optional GitHub sandbox adapter in this instance (Super Admin). */
export type GitHubIntegration = { enabled: boolean; toolId: string | null };

export type AdminHealth = { activeSuperAdmins: number; infosecMembers: number; recoveryRequired: boolean; warning: boolean };

export interface DemoResponse {
  accounts: { key: string; email: string; role: string }[];
}

// ---- Company catalog and onboarding workbook (M17) --------------------------------

export type CatalogEntry = { id: string; name: string; active: boolean };
export type CompanyCatalog = { departments: CatalogEntry[]; jobPositions: CatalogEntry[] };

export type WorkbookProblem = {
  sheet?: "Employees" | "Tools" | "Existing Access";
  row?: number;
  column?: string;
  value?: string;
  reason: string;
  expected?: string;
  otherRow?: number;
};

export type WorkbookPlan = {
  summary: {
    employeesNew: number; employeesExisting: number; toolsNew: number; toolsExisting: number;
    levelsNew: number; levelsExisting: number; accessNew: number; accessExisting: number;
  };
  owners: { tool: string; ownerEmail: string }[];
  problems: WorkbookProblem[];
  warnings: WorkbookProblem[];
};

export type WorkbookResult = { plan: WorkbookPlan; report: ImportReport };
