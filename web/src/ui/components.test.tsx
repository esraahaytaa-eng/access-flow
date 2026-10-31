import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import { ApiError } from "../api/client";
import { ConfirmDialog, ToastProvider, useToast } from "./components";

function renderDialog(onConfirm: (reason: string) => Promise<unknown>, onClose = vi.fn()) {
  render(
    <QueryClientProvider client={new QueryClient()}>
      <ToastProvider>
        <ConfirmDialog open onClose={onClose} title="Askıya al" reason onConfirm={onConfirm} confirmLabel="Askıya al" />
      </ToastProvider>
    </QueryClientProvider>,
  );
  return { onClose, confirm: () => screen.getAllByRole("button", { name: "Askıya al" }).at(-1)! };
}

describe("ConfirmDialog", () => {
  it("requires a reason before calling the server", async () => {
    const onConfirm = vi.fn();
    const { confirm } = renderDialog(onConfirm);
    await userEvent.click(confirm());
    expect(screen.getByText("Gerekçe en az 5 karakter olmalı.")).toBeTruthy();
    expect(onConfirm).not.toHaveBeenCalled();
  });

  it("disables the confirm button while the request is in flight", async () => {
    let finish!: () => void;
    const onConfirm = vi.fn(() => new Promise<void>((r) => (finish = r)));
    const { confirm, onClose } = renderDialog(onConfirm);
    await userEvent.type(screen.getByLabelText(/Gerekçe/), "  izinli personel  ");
    await userEvent.click(confirm());
    expect(onConfirm).toHaveBeenCalledWith("izinli personel");
    expect((confirm() as HTMLButtonElement).disabled).toBe(true);
    finish();
    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });

  it("stays open and shows the Turkish error when the server refuses", async () => {
    const { confirm, onClose } = renderDialog(() => Promise.reject(new ApiError(409, "nomination_pending")));
    await userEvent.type(screen.getByLabelText(/Gerekçe/), "yeterli gerekçe");
    await userEvent.click(confirm());
    expect(await screen.findByText("İşlem tamamlanamadı")).toBeTruthy();
    expect(onClose).not.toHaveBeenCalled();
  });
});

function ToastButton({ type, title }: { type: "success" | "error"; title: string }) {
  const toast = useToast();
  return <button type="button" onClick={() => toast({ type, title })}>göster</button>;
}

describe("toasts", () => {
  // Success toasts are small and short-lived so they never sit on a drawer's action buttons;
  // errors stay longer and can be closed (task-claim UX, 2026-10-31).
  const show = (type: "success" | "error", title: string) => {
    render(<ToastProvider><ToastButton type={type} title={title} /></ToastProvider>);
    fireEvent.click(screen.getByRole("button", { name: "göster" }));
  };

  it("announces a success politely and removes it after about 2.5 seconds", () => {
    vi.useFakeTimers();
    try {
      show("success", "Görev üstlenildi");
      const region = screen.getByRole("status");
      expect(region.getAttribute("aria-live")).toBe("polite");
      expect(region.textContent).toContain("Görev üstlenildi");
      act(() => { vi.advanceTimersByTime(2000); });
      expect(screen.queryByText("Görev üstlenildi")).toBeTruthy();
      act(() => { vi.advanceTimersByTime(1000); });
      expect(screen.queryByText("Görev üstlenildi")).toBeNull();
    } finally {
      vi.useRealTimers();
    }
  });

  it("keeps an error for 10 seconds unless it is closed", () => {
    vi.useFakeTimers();
    try {
      show("error", "İşlem tamamlanamadı");
      act(() => { vi.advanceTimersByTime(9000); });
      expect(screen.queryByText("İşlem tamamlanamadı")).toBeTruthy();
      act(() => { vi.advanceTimersByTime(1500); });
      expect(screen.queryByText("İşlem tamamlanamadı")).toBeNull();
    } finally {
      vi.useRealTimers();
    }
  });

  it("closes a toast from its keyboard-reachable close button", () => {
    show("error", "İşlem tamamlanamadı");
    const close = screen.getByRole("button", { name: "Bildirimi kapat" });
    close.focus();
    fireEvent.click(close);
    expect(screen.queryByText("İşlem tamamlanamadı")).toBeNull();
  });
});
