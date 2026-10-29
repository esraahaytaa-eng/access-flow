// One query per list endpoint, shared by the shell and the pages (TanStack Query caches by key).
import { useQuery } from "@tanstack/react-query";
import { api } from "./client";
import type {
  AdminGroupsResponse, AdminHealth, ApprovalsResponse, BasicEmployee, CatalogResponse, DemoResponse, Employee, EmployeesResponse, Exceptions,
  FulfillmentTasksResponse, GrantsResponse, NominationsResponse, NotificationsResponse, RequestsResponse, ReviewPage, StarterPackagesResponse,
} from "./types";

export const useEmployees = () => useQuery({ queryKey: ["employees"], queryFn: () => api.get<EmployeesResponse>("/employees").then((r) => r.employees as (Employee | BasicEmployee)[]) });
export const useTools = () => useQuery({ queryKey: ["tools"], queryFn: () => api.get<CatalogResponse>("/tools").then((r) => r.tools) });
export const useApprovals = (enabled = true) => useQuery({ queryKey: ["approvals"], enabled, queryFn: () => api.get<ApprovalsResponse>("/approvals").then((r) => r.approvals) });
export const useTasks = (enabled = true) => useQuery({ queryKey: ["tasks"], enabled, queryFn: () => api.get<FulfillmentTasksResponse>("/fulfillment-tasks").then((r) => r.tasks) });
export const useExceptions = (enabled = true) => useQuery({ queryKey: ["exceptions"], enabled, queryFn: () => api.get<Exceptions>("/exceptions") });
export const useNotifications = () => useQuery({ queryKey: ["notifications"], queryFn: () => api.get<NotificationsResponse>("/notifications").then((r) => r.notifications) });
export const useRequests = () => useQuery({ queryKey: ["requests"], queryFn: () => api.get<RequestsResponse>("/requests").then((r) => r.requests) });
export const useMyGrants = () => useQuery({ queryKey: ["access"], queryFn: () => api.get<GrantsResponse>("/access").then((r) => r.grants) });
export const useTeamGrants = () => useQuery({ queryKey: ["access", "team"], queryFn: () => api.get<GrantsResponse>("/access/team").then((r) => r.grants) });
export const useAdminGroups = () => useQuery({ queryKey: ["admin-groups"], queryFn: () => api.get<AdminGroupsResponse>("/admin-groups").then((r) => r.groups) });
export const useStarterPackages = () => useQuery({ queryKey: ["starter-packages"], queryFn: () => api.get<StarterPackagesResponse>("/starter-packages").then((r) => r.entries) });

export const useNominations = (enabled = true) => useQuery({ queryKey: ["nominations"], enabled, queryFn: () => api.get<NominationsResponse>("/super-admin-nominations").then((r) => r.nominations) });
export const useAdminHealth = (enabled = true) => useQuery({ queryKey: ["admin-health"], enabled, queryFn: () => api.get<AdminHealth>("/admin/health") });

export const useReviews = (enabled = true) => useQuery({ queryKey: ["reviews"], enabled, queryFn: () => api.get<ReviewPage>("/reviews") });

// Demo mode (local only): the server lists the demo accounts; it answers 404 otherwise.
export const useDemo = () => useQuery({ queryKey: ["demo"], staleTime: Infinity, queryFn: () => api.get<DemoResponse>("/demo") });

export const exceptionCount = (x: Exceptions) =>
  x.failedOperations.length + x.unroutableSteps.length + x.overlaps.length + x.suspensionGaps.length + x.securityRevocations.length + x.staleGrants.length +
  x.reviewFindings.length + x.orphanAccounts.length;
