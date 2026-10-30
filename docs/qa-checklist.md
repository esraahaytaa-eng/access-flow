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

## 11. Account activation (M18)

Use a private window (or a second browser) for the employee side, so the Super Admin stays signed in. Links are revealed in Yönetim ayarları → E-posta kutusu ("Bağlantıyı göster"); every reveal is audited.

**Seven demo accounts**
- [ ] On a fresh start all seven accounts sign in with the README passwords.
- [ ] Super Admin → Yönetim ayarları → E-posta kutusu: no activation e-mail exists yet (the seed sends none).
- [ ] Hesap etkinleştirme: none of the seven accounts is listed; the other demo employees are under "Bağlantı yok".

**Excel import**
- [ ] After the import in section 9, the new employee has one e-mail "AccessFlow hesabınızı etkinleştirin" in E-posta kutusu, with the badge "Etkinleştirme". The e-mail text contains no link.
- [ ] Hesap etkinleştirme → Bekleyen lists the employee with reason "Excel içe aktarma" and an expiry 24 hours ahead.

**Manual creation and rehire**
- [ ] HR Manager (ayse.demir): Yeni çalışan → the result says the link was sent to the Mock Email Outbox, valid 24 hours, single use. No password is shown anywhere.
- [ ] HR Manager: Levent Uysal (terminated) → Yeniden işe al → a new activation e-mail appears for him (reason "Yeniden işe alım").

**Who may see the links**
- [ ] HR Manager, InfoSec, Auditor, Manager and Employee: no Yönetim ayarları menu, and http://localhost:8080/settings does not open it.
- [ ] Super Admin: opening an activation e-mail shows "Bağlantıyı göster"; the revealed link is `http://localhost:8080/sifre-belirle#…`. Denetim kayıtları shows "Etkinleştirme bağlantısı görüntülendi" without the link.

**Opening the link, setting the password, signing in**
- [ ] Open the link in the private window: "Şifreni Belirle" opens, and the address bar shows only `/sifre-belirle` (the token is removed).
- [ ] The page does not sign in: there is no menu, and http://localhost:8080 in the same window shows the sign-in page.
- [ ] Set a valid password (for example `violet anchor quiet 29`): "Parolanız belirlendi." and a "Giriş yap" link. Sign in with the e-mail and the new password: it works.
- [ ] Denetim kayıtları: "Hesap etkinleştirildi" for the employee; its details list `viewedBy` with the id of the Super Admin who revealed the link.

**Weak and common passwords** (on a fresh link)
- [ ] Fewer than 12 characters: "Parola 12–128 karakter olmalı." with no request sent.
- [ ] `Password1234`: "Bu parola çok yaygın; …". A password containing the e-mail name (for example `<name>-2026-x`): "Parola e-posta adresinizi veya adınızı içeremez." The link still works afterwards.

**Used, revoked and expired links**
- [ ] Open the used link again and submit: "Bağlantı geçersiz, kullanılmış veya süresi dolmuş. …". In E-posta kutusu the e-mail's badge reads "Bağlantı kullanılamaz".
- [ ] Send a new link to a pending employee (Hesap etkinleştirme → Yeni bağlantı gönder): the older link fails with the same message, and the new one works.
- [ ] Expiry (24 hours) cannot be shortened (real time only, D12) and is covered by the automated tests. If a link is older than 24 hours, it fails the same way and the employee is listed under "Süresi dolan".

**Reissuing to an activated employee (reset)**
- [ ] Sign the activated employee in, in the private window. Super Admin or HR Manager: the employee's page → "Etkinleştirme bağlantısı gönder". The dialog warns that the current password and every open session end at once; "Vazgeç" changes nothing.
- [ ] Confirm with "Bağlantı gönder": the employee's next action in the private window returns to sign-in, and the old password no longer works. The new link sets a new password.

**Rate limiting and account lockout**
- [ ] Lockout: sign in with the activated employee and a wrong password five times; the sixth attempt, even with the right password, is refused with the same message. Sending them a new link clears the lock (or wait 15 minutes).
- [ ] Rate limit: run the command below. Expect `60 401` and `1 429`. Then wait one minute before signing in again: every browser on this machine shares the same budget.

```bash
for i in $(seq 61); do curl -s -o /dev/null -w '%{http_code}\n' -X POST http://localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' -d '{"email":"nobody@accessflow.local","password":"wrong password!"}'; done | sort | uniq -c
```

**Lost mock e-mail key** (use the throwaway project `accessflow-qa`, see Setup; send one link first, so a pending link exists)
- [ ] Delete the key and restart: `docker run --rm -v accessflow-qa_appdata:/d alpine rm /d/mock-email.key`, then `docker compose -p accessflow-qa restart app`. The app log names the recovery command.
- [ ] "Bağlantıyı göster" says the link cannot be shown. Sending a link, creating an employee or importing one shows "Mock e-posta anahtarı kullanılamıyor; …", and nothing is created.
- [ ] `docker compose -p accessflow-qa exec app /accessflow reset-mail-key`, then restart the app: it reports the revoked links, and new links can be sent and used. Denetim kayıtları shows "Mock e-posta anahtarı sıfırlandı".

**Two-step verification**
- [ ] User menu → "Google Authenticator ile iki adımlı doğrulama" with a "Planlanıyor" badge: it shows only "Bu özellik geliştirme aşamasındadır." and changes nothing.
