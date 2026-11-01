// One query per list endpoint, shared by the shell and the pages (TanStack Query caches by key).
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { api } from "./client";
import type {
  AdminGroupsResponse, AdminHealth, ApprovalsResponse, GitHubIntegration, BasicEmployee, CatalogResponse, CompanyCatalog, DemoResponse, Employee, EmployeesResponse, Exceptions,
  FulfillmentTasksResponse, GrantsResponse, NominationsResponse, NotificationsResponse, RequestsResponse, ReviewPage, ReviewView, StarterPackagesResponse,
  TaskTab, TeamResponse,
} from "./types";

/** A server-paged list's query string (PERF-004); empty values are left out. */
const listQuery = (params: Record<string, string | number | undefined>) => {
  const q = new URLSearchParams(Object.entries(params).filter(([, v]) => v !== undefined && v !== "").map(([k, v]) => [k, String(v)])).toString();
  return q ? `?${q}` : "";
};

export const useEmployees = (enabled = true) => useQuery({ queryKey: ["employees"], enabled, queryFn: () => api.get<EmployeesResponse>("/employees").then((r) => r.employees as (Employee | BasicEmployee)[]) });
export const useTools = (enabled = true) => useQuery({ queryKey: ["tools"], enabled, queryFn: () => api.get<CatalogResponse>("/tools").then((r) => r.tools) });
export const useApprovals = (enabled = true) => useQuery({ queryKey: ["approvals"], enabled, queryFn: () => api.get<ApprovalsResponse>("/approvals").then((r) => r.approvals) });
export type TaskParams = { tab?: TaskTab; q?: string; page?: number };
export const useTasks = (params: TaskParams, enabled = true) => useQuery({ queryKey: ["tasks", params], enabled, placeholderData: keepPreviousData,
  queryFn: () => api.get<FulfillmentTasksResponse>(`/fulfillment-tasks${listQuery(params)}`) });
export const useExceptions = (enabled = true, staleTime = 0) => useQuery({ queryKey: ["exceptions"], enabled, staleTime, queryFn: () => api.get<Exceptions>("/exceptions") });
export const useNotifications = (enabled = true) => useQuery({ queryKey: ["notifications"], enabled, queryFn: () => api.get<NotificationsResponse>("/notifications").then((r) => r.notifications) });
export const useRequests = () => useQuery({ queryKey: ["requests"], queryFn: () => api.get<RequestsResponse>("/requests").then((r) => r.requests) });
export const useMyGrants = () => useQuery({ queryKey: ["access"], queryFn: () => api.get<GrantsResponse>("/access").then((r) => r.grants) });
export const useTeam = (params: { q?: string; page?: number }) => useQuery({ queryKey: ["access", "team", params], placeholderData: keepPreviousData,
  queryFn: () => api.get<TeamResponse>(`/access/team${listQuery(params)}`) });
/** A direct report's grants; nothing for anyone else. */
export const useTeamMemberGrants = (id: string | null) => useQuery({ queryKey: ["access", "team", "member", id], enabled: !!id,
  queryFn: () => api.get<GrantsResponse>(`/access/team/${id}`).then((r) => r.grants) });
export const useAdminGroups = () => useQuery({ queryKey: ["admin-groups"], queryFn: () => api.get<AdminGroupsResponse>("/admin-groups").then((r) => r.groups) });
export const useCompanyCatalog = () => useQuery({ queryKey: ["company-catalog"], queryFn: () => api.get<CompanyCatalog>("/company-catalog") });
export const useStarterPackages = () => useQuery({ queryKey: ["starter-packages"], queryFn: () => api.get<StarterPackagesResponse>("/starter-packages").then((r) => r.entries) });

export const useNominations = (enabled = true) => useQuery({ queryKey: ["nominations"], enabled, queryFn: () => api.get<NominationsResponse>("/super-admin-nominations").then((r) => r.nominations) });
export const useAdminHealth = (enabled = true) => useQuery({ queryKey: ["admin-health"], enabled, queryFn: () => api.get<AdminHealth>("/admin/health") });
export const useGitHubIntegration = () => useQuery({ queryKey: ["integrations", "github"], queryFn: () => api.get<GitHubIntegration>("/integrations/github") });

export type ReviewParams = { view?: ReviewView; q?: string; page?: number };
export const useReviews = (params: ReviewParams = {}, enabled = true, staleTime = 0) => useQuery({ queryKey: ["reviews", params], enabled, staleTime,
  placeholderData: keepPreviousData, queryFn: () => api.get<ReviewPage>(`/reviews${listQuery(params)}`) });

// Demo mode (local only): the server lists the demo accounts; it answers 404 otherwise.
export const useDemo = () => useQuery({ queryKey: ["demo"], staleTime: Infinity, queryFn: () => api.get<DemoResponse>("/demo") });

export const exceptionCount = (x: Exceptions) =>
  Object.values(x.totals).reduce((a, b) => a + b, 0) + x.reviewFindingsTotal;
