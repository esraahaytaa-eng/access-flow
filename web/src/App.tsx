import { QueryCache, QueryClient, QueryClientProvider, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState, type ReactNode } from "react";
import { BrowserRouter, Navigate, Route, Routes } from "react-router";
import { api, ApiError, setUnauthorizedHandler } from "./api/client";
import type { Principal } from "./api/types";
import { ChangePasswordPage, ForcedPasswordChange } from "./pages/ChangePassword";
import { AuditPage } from "./pages/Audit";
import { ApprovalsPage } from "./pages/Approvals";
import { ReviewsPage } from "./pages/Reviews";
import { CatalogPage } from "./pages/Catalog";
import { EmployeeDetailPage } from "./pages/EmployeeDetail";
import { DirectGrantPage } from "./pages/DirectGrant";
import { EmployeesPage } from "./pages/Employees";
import { ExceptionsPage } from "./pages/Exceptions";
import { HomePage } from "./pages/Home";
import { MyAccessPage } from "./pages/MyAccess";
import { MyRequestsPage } from "./pages/MyRequests";
import { NewRequestPage } from "./pages/NewRequest";
import { RequestDetailPage } from "./pages/RequestDetail";
import { SettingsPage } from "./pages/Settings";
import { MailboxPage } from "./pages/Mailbox";
import { RoleAcceptPage } from "./pages/RoleAccept";
import { TasksPage } from "./pages/Tasks";
import { TeamPage } from "./pages/Team";
import { ToolEditPage } from "./pages/ToolEdit";
import { ToolsAdminPage } from "./pages/ToolsAdmin";
import { AuthScreen, LoginPage } from "./pages/Login";
import { SetPasswordPage } from "./pages/SetPassword";
import { SessionProvider, useSession } from "./session";
import { NoAccess, NotFound, Shell } from "./Shell";
import { errorMessage } from "./i18n/errors";
import { useEmployees } from "./api/queries";
import { useRoleFlags } from "./nav";
import { ToastProvider, useToast } from "./ui/components";

const isClientError = (e: unknown) => e instanceof ApiError && e.status >= 400 && e.status < 500;

function newQueryClient() {
  const qc: QueryClient = new QueryClient({
    // A temporary password blocks every route but /auth/*: re-read the session to show the change form.
    queryCache: new QueryCache({ onError: (e) => { if (e instanceof ApiError && e.code === "password_change_required") qc.invalidateQueries({ queryKey: ["me"] }); } }),
    defaultOptions: { queries: { retry: (n, e) => !isClientError(e) && n < 2 } },
  });
  return qc;
}

const fetchMe = () => api.get<Principal>("/auth/me").catch((e) => { if (e instanceof ApiError && e.status === 401) return null; throw e; });

/** Hides a page from roles that cannot use it; the server enforces the same rule. */
const superAdmin = (s: ReturnType<typeof useSession>) => s.hasRole("SUPER_ADMIN");

function Guard({ allow, children }: { allow: (s: ReturnType<typeof useSession>) => boolean; children: ReactNode }) {
  return allow(useSession()) ? children : <NoAccess />;
}

/** Managers see the audit page for their reports, known once the directory has loaded. */
function AuditGuard({ children }: { children: ReactNode }) {
  const allowed = useRoleFlags().audit;
  const pending = useEmployees().isPending;
  return allowed ? children : pending ? null : <NoAccess />;
}

function Root() {
  const qc = useQueryClient();
  const toast = useToast();
  const me = useQuery({ queryKey: ["me"], queryFn: fetchMe, staleTime: Infinity });
  const setPrincipal = (p: Principal | null) => { qc.removeQueries({ predicate: (q) => q.queryKey[0] !== "me" }); qc.setQueryData(["me"], p); };
  useEffect(() => setUnauthorizedHandler(() => setPrincipal(null)), []); // eslint-disable-line react-hooks/exhaustive-deps
  // A failed logout leaves the server session alive, so the user stays signed in and is told (a 401 already signs out).
  const signOut = () => api.post("/auth/logout").then(() => setPrincipal(null), (err) => toast({ type: "error", title: "Çıkış yapılamadı", desc: errorMessage(err) }));

  // An activation link works whoever is signed in here and never signs anyone in (M18).
  if (window.location.pathname === "/sifre-belirle") return <SetPasswordPage />;
  if (me.isPending) return null;
  if (me.isError) return <AuthScreen><div className="login-card" role="alert"><h1>Sunucuya ulaşılamıyor</h1><p className="muted">Sayfayı yenileyin.</p></div></AuthScreen>;
  if (!me.data) return <LoginPage onSignedIn={setPrincipal} />;
  if (me.data.mustChangePassword) return <ForcedPasswordChange onChanged={setPrincipal} onSignOut={signOut} />;
  return (
    <SessionProvider principal={me.data}>
      <Shell onSignOut={signOut}>
        {me.data.employee.roles.includes("AUDITOR") ? (
          // An external auditor reads only the audit evidence on Denetim kayıtları, whatever other roles they hold (O7 notes 2026-10-31).
          <Routes>
            <Route path="/" element={<Navigate to="/audit" replace />} />
            <Route path="/audit" element={<AuditPage />} />
            <Route path="/password" element={<ChangePasswordPage onChanged={setPrincipal} />} />
            <Route path="*" element={<NoAccess />} />
          </Routes>
        ) : (
        <Routes>
          <Route path="/" element={<HomePage />} />
          <Route path="/catalog" element={<CatalogPage />} />
          <Route path="/request-new" element={<NewRequestPage />} />
          <Route path="/requests" element={<MyRequestsPage />} />
          <Route path="/requests/:id" element={<RequestDetailPage />} />
          <Route path="/access" element={<MyAccessPage />} />
          <Route path="/approvals" element={<ApprovalsPage />} />
          <Route path="/team" element={<TeamPage />} />
          <Route path="/reviews" element={<ReviewsPage />} />
          <Route path="/tasks" element={<Guard allow={(s) => s.adminGroups.length > 0}><TasksPage /></Guard>} />
          <Route path="/employees" element={<Guard allow={(s) => s.hasRole("HR_MANAGER", "SUPER_ADMIN")}><EmployeesPage /></Guard>} />
          <Route path="/employees/:id" element={<Guard allow={(s) => s.hasRole("HR_MANAGER", "SUPER_ADMIN")}><EmployeeDetailPage /></Guard>} />
          <Route path="/exceptions" element={<Guard allow={(s) => s.hasRole("INFOSEC", "SUPER_ADMIN")}><ExceptionsPage /></Guard>} />
          <Route path="/tools-admin" element={<Guard allow={superAdmin}><ToolsAdminPage /></Guard>} />
          <Route path="/tools-admin/:id" element={<Guard allow={superAdmin}><ToolEditPage /></Guard>} />
          <Route path="/direct" element={<Guard allow={superAdmin}><DirectGrantPage /></Guard>} />
          <Route path="/settings" element={<Guard allow={superAdmin}><SettingsPage /></Guard>} />
          <Route path="/audit" element={<AuditGuard><AuditPage /></AuditGuard>} />
          <Route path="/password" element={<ChangePasswordPage onChanged={setPrincipal} />} />
          <Route path="/mailbox" element={<MailboxPage />} />
          <Route path="/rol-kabul" element={<RoleAcceptPage />} />
          <Route path="*" element={<NotFound />} />
        </Routes>
        )}
      </Shell>
    </SessionProvider>
  );
}

export function App() {
  const [qc] = useState(newQueryClient);
  return (
    <QueryClientProvider client={qc}>
      <ToastProvider>
        <BrowserRouter><Root /></BrowserRouter>
      </ToastProvider>
    </QueryClientProvider>
  );
}

