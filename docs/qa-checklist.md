# Browser QA Checklist

A reproducible manual check of the main flows in a real browser, on a clean start. Run it in a current Chrome or Firefox, at desktop width and once at phone width (DevTools, 390 px). Every step names the expected result; note any difference.

## Setup

```bash
docker compose up
```

Open http://localhost:8080. The demo accounts and passwords are in the README ("Demo accounts"). Sign out (user menu → Çıkış yap) between roles.

The checklist expects the demo data as seeded on a fresh database. Your existing local data is never deleted for QA. If the app log says `demo seed: the database holds data from an interrupted start or an earlier version`, run the checklist in a separate, throwaway Compose project instead; it has its own volumes and leaves yours untouched:

```bash
docker compose down                        # stop the main project (keeps its volumes)
docker compose -p accessflow-qa up         # fresh volumes named accessflow-qa_*
# after QA, remove only the throwaway project's volumes:
docker compose -p accessflow-qa down -v
```

## 1. Sign-in page

- [ ] One centered card: logo, "Oturum açın", e-mail, password, "Oturum aç". No side panel or marketing text.
- [ ] "Demo Accounts" lists seven accounts with their English role ("Super Admin" … "Employee") and e-mail, the line "Select an account. Passwords are listed in the README.", and an amber "Demo Environment" badge. No password is shown.
- [ ] Clicking an account fills the e-mail and moves the focus to the password.
- [ ] "Oturum aç" with empty fields shows "E-posta adresinizi girin." and "Parolanızı girin."; no request is sent (DevTools → Network).
- [ ] A wrong password shows "E-posta adresi veya parola hatalı."
- [ ] Keyboard only: Tab reaches the fields, the account buttons and the submit button; Enter submits.
- [ ] Phone width: the card fits without horizontal scrolling; account rows stack the role over the e-mail.

## 2. Every signed-in page

- [ ] The top bar shows the "Demo Environment" badge.
- [ ] No recovery banner: an active Super Admin exists (the banner appears only when none is left, D2).

## 3. Employee — deniz.kara

- [ ] Erişimlerim: Jira (User) and GitHub (Developer) active.
- [ ] Erişim talep et → a level from Araç kataloğu, with a reason and a duration → the request appears in Taleplerim with its steps.

## 4. Manager — burak.sahin

- [ ] Onay kutusu: Mert Acar's AWS Console / Admin waits at the Manager step. Approve it: it moves to the Tool Owner step.
- [ ] Ekibim: the team's access, including Elif Çelik's direct Figma grant (temporary).

## 5. Tool Owner — can.ozturk

- [ ] Onay kutusu: Elif Çelik's GitHub request waits at the Tool Owner step.
- [ ] Karşılama görevleri: the open Manual tasks of IT Operations. Claim one and confirm it: the grant becomes active, labelled self-reported.
- [ ] Erişim incelemeleri: the running campaign; Deniz Kara's Jira item is already kept.

## 6. InfoSec — selin.aydin

- [ ] Onay kutusu: Zeynep Yıldız's Jira / Admin waits at the InfoSec step.
- [ ] İstisnalar: one failed operation (Mert Acar's GitHub task), two orphan accounts (eski.stajyer on Jira, ajans@partner-reklam.com on Salesforce). Revoke one orphan with a reason.

## 7. HR Manager — ayse.demir

- [ ] Çalışanlar: Oya Sezer suspended, Levent Uysal terminated (removal pending), Tuna Erdem with a scheduled termination.
- [ ] Transfer an employee to another manager: the transfer screen pre-selects their access for removal.
- [ ] Çalışanlar → Departmanlar ve unvanlar: eight departments and 18 positions in English. Add "Legal", then retire it: it shows "(kullanımdan kaldırıldı)" and is no longer offered in Yeni çalışan. Adding "legal" again is refused with the defined spelling.
- [ ] Yeni çalışan: Departman and Unvan are pickers with the catalog names only.
- [ ] Karşılama görevleri (Vendor Desk): Ece Polat's Payroll Portal task shows the vendor ticket TDR-2026-0142, "Tedarikçi bekleniyor".

## 8. Auditor — gizem.arslan

- [ ] Denetim kayıtları: events since startup, filters by action and person; CSV export downloads the loaded rows.
- [ ] Point-in-time access for today, and the leavers, overrides, policy, privileged and quarter reports open.
- [ ] No action buttons (read-only).

## 9. Super Admin — admin

- [ ] Araç yönetimi: six tools, with API, Manual and Vendor methods.
- [ ] Doğrudan erişim: a duration outside 2–90 days shows "2 ile 90 gün arasında bir süre girin."
- [ ] Yönetim ayarları: roles and admin groups; nominating a second Super Admin needs a different InfoSec member to authorize it.
- [ ] İçe aktarma → Excel şablonunu indir: the file opens in Excel and in LibreOffice Calc with three sheets (Employees, Tools, Existing Access), a grey example row in each and dropdowns for Department, Job Position, Fulfillment Method, Admin Group and Approval Steps.
- [ ] Önizle the untouched template: three "Örnek satır" problems and "Excel’i içe aktar" stays disabled.
- [ ] Replace the example rows with one new employee (catalog department and position, manager burak.sahin@accessflow.local), one new tool with two levels (owner can.ozturk@accessflow.local, admin group IT Operations) and one access row for the new employee on that tool. Önizle shows no problem, the tool owner and the counts; Excel’i içe aktar creates them all. InfoSec (selin.aydin) gets one notification for the new approval policies.
- [ ] Import the same file again: nothing new is created; the access row counts as already imported.
- [ ] Change the new employee's name in the file and preview: a warning, no problem; the stored name stays.
- [ ] Put an unknown department in a row: the preview lists the sheet, row, column and value, and nothing is imported.
- [ ] Araç servisinden içe aktar after the workbook: the report shows how many reported API grants were verified and how many the tool does not hold.

## 10. Restart

- [ ] `docker compose restart app`, then sign in again: the same data, nothing duplicated.
