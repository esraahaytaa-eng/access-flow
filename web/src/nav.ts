// Who sees which page (design/ spec 2.2). Convenience only: the server enforces every permission.
import { exceptionCount, useApprovals, useEmployees, useExceptions, useTasks, useTools } from "./api/queries";
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
    exceptions: infosec || superAdmin || auditor,
    employees: hr || superAdmin,
    admin: superAdmin,
  };
}

export type RoleFlags = ReturnType<typeof useRoleFlags>;

export function useNavCounts(flags: RoleFlags) {
  const approvals = useApprovals().data?.length ?? 0;
  const tasks = useTasks(flags.tasks).data?.filter((t) => t.canAct).length ?? 0;
  const ex = useExceptions(flags.exceptions).data;
  return { approvals, tasks, exceptions: ex ? exceptionCount(ex) : 0 };
}
