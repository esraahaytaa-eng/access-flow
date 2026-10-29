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

/** Shown once; the response carries Cache-Control: no-store. */
export type EmployeeWithPassword = { employee: Employee; temporaryPassword: string };

export type PasswordResetResponse = { temporaryPassword: string };

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

/** Catalog tool. status/version only for Super Admin, InfoSec and Auditor. */
export type CatalogTool = {
  id: string;
  name: string;
  fulfillmentMethod: FulfillmentMethod;
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
  status: CatalogStatus;
  version: number;
};

/** Administrative level view returned by the level mutations. */
export type AccessLevel = {
  id: string;
  toolId: string;
  name: string;
  requiresToolOwner: boolean;
  requiresInfosec: boolean;
  steps: StepType[];
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
export type Approval = { step: ApprovalStep; item: RequestItem; unroutable: boolean };
export type ApprovalsResponse = { approvals: Approval[] };

export type Email = {
  id: string;
  recipient: Person;
  requestId: string;
  subject: string;
  body: string;
  createdAt: string;
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
  version: number;
  createdAt: string;
};

export type GrantsResponse = { grants: Grant[] };

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
};

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
  employee: Person;
  requestId: string;
  itemId: string;
  ticketRef: string | null;
  claimedBy: Person | null;
  /** false on the viewer's own grant or a direct action they initiated. */
  canAct: boolean;
  version: number;
  createdAt: string;
};

export type FulfillmentTasksResponse = { tasks: FulfillmentTask[] };

// ---- Notifications ----------------------------------------------------------------

export type NotificationKind =
  | "APPROVAL_POLICY_CHANGED"
  | "DIRECT_GRANT"
  | "SUPER_ADMIN_OVERRIDE"
  | "APPROVAL_ASSIGNED"
  | "GRANTS_RETAINED_ON_TRANSFER";

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
