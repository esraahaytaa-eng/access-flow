import { describe, expect, it } from "vitest";
import { reasonText, routingNoteText } from "./reasons";

describe("reasonText", () => {
  it.each([
    ["transferred", "Çalışan transfer edildi"],
    ["access level retired", "Erişim seviyesi kullanımdan kaldırıldı"],
    ["expired before approval", "Onaylanmadan süresi doldu"],
  ])("translates the system reason %s", (code, text) => {
    expect(reasonText(code)).toBe(text);
  });

  it("shows a reason typed by a person unchanged", () => {
    expect(reasonText("Proje bitti")).toBe("Proje bitti");
  });
});

describe("routingNoteText", () => {
  it.each([
    ["manager inactive → manager's manager", "Yönetici aktif değil → yöneticinin yöneticisi"],
    ["manager is the submitter, manager's manager missing → InfoSec", "Yönetici talebi gönderen kişi, yöneticinin yöneticisi yok → Bilgi Güvenliği"],
    ["tool owner is an earlier approver → InfoSec", "Araç sahibi önceki bir onaylayıcı → Bilgi Güvenliği"],
    ["reassigned: izinde", "Yeniden atandı: izinde"],
    ["transferred", "Çalışan transfer edildi"],
  ])("translates %s", (note, text) => {
    expect(routingNoteText(note)).toBe(text);
  });

  it("hides the normal route", () => {
    expect(routingNoteText("manager")).toBeNull();
    expect(routingNoteText("tool owner")).toBeNull();
    expect(routingNoteText("InfoSec")).toBeNull();
  });
});
