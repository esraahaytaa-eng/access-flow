// Who sees which page (design/ spec 2.2). Convenience only: the server enforces every permission.
import { exceptionCount, useApprovals, useEmployees, useExceptions, useNominations, useReviews, useTasks, useTools } from "./api/queries";
import { useSession } from "./session";

export function useRoleFlags() {
  const { me, adminGroups, hasRole } = useSession();
  const employees = useEmployees().data ?? [];
  const tools = useTools().data ?? [];
  const manager = employees.some((e) => e.managerId === me.id && (!("status" in e) || e.status !== "TERMINATED"));
  const owner = tools.some((t) => t.owner.id === me.id);
  const superAdmin = hasRole("SUPER_ADMIN"), infosec = hasRole("INFOSEC"), hr = hasRole("HR_MANAGER"), auditor = hasRole("AUDITOR");
  const approvals = useApprovals().data ?? [];
  return {
    manager, owner, superAdmin, infosec, hr, auditor,
    approvals: manager || owner || infosec || superAdmin || approvals.length > 0,
    tasks: adminGroups.length > 0,
    reviews: owner || infosec || superAdmin || auditor,
    exceptions: infosec || superAdmin || auditor,
    employees: hr || superAdmin,
    admin: superAdmin,
    audit: superAdmin || infosec || auditor || manager,
  };
}

export type RoleFlags = ReturnType<typeof useRoleFlags>;

export function useNavCounts(flags: RoleFlags) {
  const nominations = useNominations(flags.superAdmin || flags.infosec).data?.filter((n) => n.canAuthorize).length ?? 0;
  const approvals = (useApprovals().data?.length ?? 0) + nominations;
  const tasks = useTasks(flags.tasks).data?.filter((t) => t.canAct).length ?? 0;
  const ex = useExceptions(flags.exceptions).data;
  const { me } = useSession();
  const reviews = useReviews(flags.reviews).data?.items.filter((i) => i.reviewerId === me.id && !i.decision && !i.closedReason).length ?? 0;
  return { approvals, tasks, reviews, exceptions: ex ? exceptionCount(ex) : 0 };
}
