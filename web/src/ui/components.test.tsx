import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import { ApiError } from "../api/client";
import { ConfirmDialog, ToastProvider } from "./components";

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
