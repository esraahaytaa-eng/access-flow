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
- [ ] "Demo hesapları" lists seven accounts with role and e-mail, and an amber "Demo ortamı" badge. No password is shown.
- [ ] Clicking an account fills the e-mail and moves the focus to the password.
- [ ] "Oturum aç" with empty fields shows "E-posta adresinizi girin." and "Parolanızı girin."; no request is sent (DevTools → Network).
- [ ] A wrong password shows "E-posta adresi veya parola hatalı."
- [ ] Keyboard only: Tab reaches the fields, the account buttons and the submit button; Enter submits.
- [ ] Phone width: the card fits without horizontal scrolling; account rows stack the role over the e-mail.

## 2. Every signed-in page

- [ ] The top bar shows the "Demo ortamı" badge.
- [ ] No recovery banner: an active Super Admin exists (the banner appears only when none is left, D2).

## 3. Employee — deniz.kara

- [ ] Erişimlerim: Jira (Kullanıcı) and GitHub (Geliştirici) active.
- [ ] Erişim talep et → a level from Araç kataloğu, with a reason and a duration → the request appears in Taleplerim with its steps.

## 4. Manager — burak.sahin

- [ ] Onay kutusu: Mert Acar's AWS Konsolu / Yönetici waits at the Manager step. Approve it: it moves to the Tool Owner step.
- [ ] Ekibim: the team's access, including Elif Çelik's direct Figma grant (temporary).

## 5. Tool Owner — can.ozturk

- [ ] Onay kutusu: Elif Çelik's GitHub request waits at the Tool Owner step.
- [ ] Karşılama görevleri: the open Manual tasks of BT Operasyon. Claim one and confirm it: the grant becomes active, labelled self-reported.
- [ ] Erişim incelemeleri: the running campaign; Deniz Kara's Jira item is already kept.

## 6. InfoSec — selin.aydin

- [ ] Onay kutusu: Zeynep Yıldız's Jira / Yönetici waits at the InfoSec step.
- [ ] İstisnalar: one failed operation (Mert Acar's GitHub task), two orphan accounts (eski.stajyer on Jira, ajans@partner-reklam.com on Salesforce). Revoke one orphan with a reason.

## 7. HR Manager — ayse.demir

- [ ] Çalışanlar: Oya Sezer suspended, Levent Uysal terminated (removal pending), Tuna Erdem with a scheduled termination.
- [ ] Transfer an employee to another manager: the transfer screen pre-selects their access for removal.
- [ ] Karşılama görevleri (Tedarikçi Masası): Ece Polat's Bordro Portalı task shows the vendor ticket TDR-2026-0142, "Tedarikçi bekleniyor".

## 8. Auditor — gizem.arslan

- [ ] Denetim kayıtları: events since startup, filters by action and person; CSV export downloads the loaded rows.
- [ ] Point-in-time access for today, and the leavers, overrides, policy, privileged and quarter reports open.
- [ ] No action buttons (read-only).

## 9. Super Admin — admin

- [ ] Araç yönetimi: six tools, with API, Manual and Vendor methods.
- [ ] Doğrudan erişim: a duration outside 2–90 days shows "2 ile 90 gün arasında bir süre girin."
- [ ] Yönetim ayarları: roles and admin groups; nominating a second Super Admin needs a different InfoSec member to authorize it.

## 10. Restart

- [ ] `docker compose restart app`, then sign in again: the same data, nothing duplicated.
