// Minimal API records for page tests.
export const person = (id: string, fullName: string) => ({ id, fullName });

export const step = (over: Record<string, unknown> = {}) => ({
  id: "s1", position: 1, type: "MANAGER", status: "PENDING", assigneeId: "m1", assigneeName: "Burak Manager", routingNote: "manager",
  openedAt: "2026-10-28T09:00:00Z", decidedBy: null, decidedAt: null, reason: null, version: 3, ...over,
});

export const item = (over: Record<string, unknown> = {}) => ({
  id: "i1", requestId: "r1", beneficiary: person("e1", "Elif Yılmaz"), submittedBy: person("e1", "Elif Yılmaz"),
  toolId: "t1", tool: "Jira", accessLevelId: "l1", level: "Kullanıcı", justification: "Proje için gerekli erişim.",
  duration: { type: "UNTIL_REVOKED", expiresOn: null }, status: "PENDING_APPROVAL", version: 4, createdAt: "2026-10-28T09:00:00Z",
  closedAt: null, closedReason: null, steps: [step()], fulfillment: null, ...over,
});

export const request = (items = [item()], over: Record<string, unknown> = {}) => ({
  id: "r1", submissionId: "x", kind: "NEW_ACCESS", beneficiary: person("e1", "Elif Yılmaz"), submittedBy: person("e1", "Elif Yılmaz"),
  createdAt: "2026-10-28T09:00:00Z", items, ...over,
});

export const grant = (over: Record<string, unknown> = {}) => ({
  id: "g1", employee: person("e1", "Elif Yılmaz"), toolId: "t1", tool: "Jira", accessLevelId: "l1", level: "Kullanıcı", method: "API",
  status: "ACTIVE", displayStatus: "ACTIVE / VERIFIED", evidence: "VERIFIED", effectiveFrom: "2026-10-01T09:00:00Z", effectiveTo: null,
  expiresOn: null, closedReason: null, suspension: null, retainedOnTransfer: null, imported: false, version: 7, createdAt: "2026-10-01T09:00:00Z", ...over,
});

export const employee = (over: Record<string, unknown> = {}) => ({
  id: "e2", email: "deniz@x.co", fullName: "Deniz Kara", department: "Ürün", jobTitle: "Analist", managerId: "m1", status: "ACTIVE",
  roles: [], adminGroups: [], mustChangePassword: false, termination: null, version: 5, createdAt: "2026-01-01T09:00:00Z", ...over,
});
