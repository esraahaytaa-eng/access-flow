import { fireEvent, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { Shell } from "./Shell";
import { renderPage } from "./test/render";
import { fakeServer } from "./test/server";

afterEach(() => vi.unstubAllGlobals());

describe("two-step verification modal", () => {
  // Regression: the modal was rendered inside the sticky, blurred header, whose stacking context
  // and containing block kept the backdrop and dialog under the sidebar and page content.
  it("opens above the whole app, locks the page behind it and closes again", () => {
    fakeServer({ "GET /notifications": { notifications: [] } });
    const { container } = renderPage(<Shell onSignOut={() => {}}><p>Sayfa içeriği</p></Shell>);
    container.id = "root"; // as in index.html: the app behind a dialog
    const open = () => {
      fireEvent.click(screen.getByRole("button", { name: /Elif Yılmaz/ }));
      fireEvent.click(screen.getByRole("button", { name: /iki adımlı doğrulama/i }));
      return screen.getByRole("dialog", { name: "İki adımlı doğrulama" });
    };

    const dialog = open();
    expect(container.querySelector(".topbar")?.contains(dialog)).toBe(false);
    expect(container.contains(dialog)).toBe(false); // rendered at the document level, not in the header
    expect(document.body.contains(dialog)).toBe(true);
    expect(document.querySelector(".overlay")?.parentElement).toBe(document.body);
    expect(container.hasAttribute("inert")).toBe(true); // nothing behind it can be clicked or focused
    expect(document.body.style.overflow).toBe("hidden"); // the page does not scroll behind it
    expect(dialog.contains(document.activeElement)).toBe(true);

    fireEvent.keyDown(window, { key: "Escape" });
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(document.activeElement?.getAttribute("aria-haspopup")).toBe("menu"); // focus back on the user menu
    expect(container.hasAttribute("inert")).toBe(false);
    expect(document.body.style.overflow).toBe("");

    open();
    const [closeIcon, closeButton] = screen.getAllByRole("button", { name: "Kapat" });
    expect(closeIcon).toBeTruthy();
    fireEvent.click(closeButton); // the footer button; the header icon closes the same way
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(container.hasAttribute("inert")).toBe(false);
  });
});
