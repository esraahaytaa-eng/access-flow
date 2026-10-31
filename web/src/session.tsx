import { createContext, useContext, type ReactNode } from "react";
import type { Principal, Role } from "./api/types";

// The signed-in principal from /auth/me; pages read it instead of refetching.
const SessionContext = createContext<Principal | null>(null);

export const SessionProvider = ({ principal, children }: { principal: Principal; children: ReactNode }) =>
  <SessionContext.Provider value={principal}>{children}</SessionContext.Provider>;

export function useSession() {
  const p = useContext(SessionContext);
  if (!p) throw new Error("useSession outside a session");
  return {
    me: p.employee,
    adminGroups: p.adminGroups,
    timezone: p.companyTimezone,
    hasRole: (...roles: Role[]) => roles.some((r) => p.employee.roles.includes(r)),
    /** An external auditor reads only the audit events, whatever other roles they hold (O7 note 2026-10-31). */
    auditor: p.employee.roles.includes("AUDITOR"),
  };
}
