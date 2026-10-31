// Who sees which page (design/ spec 2.2). Convenience only: the server enforces every permission.
import { exceptionCount, useApprovals, useEmployees, useExceptions, useNominations, useReviews, useTasks, useTools } from "./api/queries";
import { useSession } from "./session";

export function useRoleFlags() {
  const { me, adminGroups, hasRole, auditor } = useSession();
  // An Auditor reads only the audit events: none of these lists is loaded for them.
  const employees = useEmployees(!auditor).data ?? [];
  const tools = useTools(!auditor).data ?? [];
  const manager = employees.some((e) => e.managerId === me.id && (!("status" in e) || e.status !== "TERMINATED"));
  const owner = tools.some((t) => t.owner.id === me.id);
  const superAdmin = !auditor && hasRole("SUPER_ADMIN"), infosec = !auditor && hasRole("INFOSEC"), hr = !auditor && hasRole("HR_MANAGER");
  const approvals = useApprovals(!auditor).data ?? [];
  if (auditor) {
    return { manager: false, owner: false, superAdmin, infosec, hr, auditor, approvals: false, tasks: false, reviews: false,
      exceptions: false, employees: false, admin: false, audit: true };
  }
  return {
    manager, owner, superAdmin, infosec, hr, auditor,
    approvals: manager || owner || infosec || superAdmin || approvals.length > 0,
    tasks: adminGroups.length > 0,
    reviews: owner || infosec || superAdmin,
    exceptions: infosec || superAdmin,
    employees: hr || superAdmin,
    admin: superAdmin,
    audit: superAdmin || infosec || manager,
  };
}

export type RoleFlags = ReturnType<typeof useRoleFlags>;

// The two large lists back badges only here: refetched at most every 30 s on focus, not on every
// focus (PERF-003); their own pages and every change still refetch them at once.
const badgeStaleTime = 30_000;

export function useNavCounts(flags: RoleFlags) {
  const nominations = useNominations(flags.superAdmin || flags.infosec).data?.filter((n) => n.canAuthorize).length ?? 0;
  const approvals = (useApprovals(!flags.auditor).data?.length ?? 0) + nominations;
  const tasks = useTasks({ tab: "queue" }, flags.tasks).data?.counts.canAct ?? 0;
  const ex = useExceptions(flags.exceptions, badgeStaleTime).data;
  const reviews = useReviews({ view: "mine" }, flags.reviews, badgeStaleTime).data?.counts.mine ?? 0;
  return { approvals, tasks, reviews, exceptions: ex ? exceptionCount(ex) : 0 };
}
