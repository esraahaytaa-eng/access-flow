import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render } from "@testing-library/react";
import type { ReactNode } from "react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router";
import type { Principal } from "../api/types";
import { SessionProvider } from "../session";
import { ToastProvider } from "../ui/components";
import { principal } from "./server";

export let currentPath = "";
function TrackPath() { currentPath = useLocation().pathname; return null; }

/** Renders one page at `path` (matched by `pattern`) with a session and fresh caches. */
export function renderPage(page: ReactNode, { path = "/", pattern = "*", session = principal() as Principal } = {}) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <ToastProvider>
        <SessionProvider principal={session}>
          <MemoryRouter initialEntries={[path]}>
            <TrackPath />
            <Routes><Route path={pattern} element={page} /><Route path="*" element={<TrackPath />} /></Routes>
          </MemoryRouter>
        </SessionProvider>
      </ToastProvider>
    </QueryClientProvider>,
  );
}
