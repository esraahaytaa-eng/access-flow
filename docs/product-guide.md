# AccessFlow — Product Analysis and Functional Guide / Ürün Analizi ve İşlevsel Kılavuz

A study guide to what AccessFlow does, who uses it, how each flow works step by step, and what people still do by hand in the external tools. Every example comes from the demo company that a fresh start creates (`internal/demo/demo.go`, `internal/demo/workflows.go`) and was checked against the current code and [DECISIONS.md](../DECISIONS.md).

> **Kısaca (TR):** Bu kılavuz AccessFlow'un ne yaptığını, kimlerin hangi işi yaptığını, her akışın adım adım nasıl ilerlediğini ve insanların dış sistemlerde (AWS, GitHub, bordro tedarikçisi) hâlâ elle ne yaptığını anlatır. Örneklerin hepsi demo şirketinden alınmıştır ve mevcut koda göre doğrulanmıştır.

**How to read it / Nasıl okunur**

- Each section starts with a short Turkish summary (**Kısaca (TR)**), followed by the details in English. The UI is in Turkish, so screen and button names are given in Turkish with the English meaning beside them.
- Capability labels used throughout:
  - **[Implemented / Uygulandı]**: works in the application today and is covered by tests.
  - **[Simulated / Simüle]**: works end to end, but against a local stand-in (the Mock Tool Service or the Mock Email Outbox), not a real system.
  - **[Manual / İnsan işi]**: done by a person outside AccessFlow; AccessFlow only records that it was done.
  - **[Future / Gelecek]**: not built; listed as a next step.
- Rule references such as D7 or O1 point to [DECISIONS.md](../DECISIONS.md), the approved rules. The requirements are in [CASE.md](../CASE.md).

---

## Contents / İçindekiler

1. [What AccessFlow is / AccessFlow nedir](#1-what-accessflow-is--accessflow-nedir)
2. [Concepts and terminology / Kavramlar ve terimler](#2-concepts-and-terminology--kavramlar-ve-terimler)
3. [Roles, permissions and responsibilities / Roller, yetkiler ve sorumluluklar](#3-roles-permissions-and-responsibilities--roller-yetkiler-ve-sorumluluklar)
4. [Admin groups, Tool Owners and the Vendor Desk / Yönetim grupları, araç sahipleri ve Tedarikçi Masası](#4-admin-groups-tool-owners-and-the-vendor-desk--yönetim-grupları-araç-sahipleri-ve-tedarikçi-masası)
5. [The three fulfillment methods, step by step / Üç karşılama yöntemi, adım adım](#5-the-three-fulfillment-methods-step-by-step--üç-karşılama-yöntemi-adım-adım)
6. [Demo walkthroughs: AWS Console, GitHub, Payroll Portal / Demo örnekleri](#6-demo-walkthroughs-aws-console-github-payroll-portal--demo-örnekleri)
7. [The access lifecycle: request → approval → provisioning → verification → revocation / Erişim yaşam döngüsü](#7-the-access-lifecycle--erişim-yaşam-döngüsü)
8. [Employee lifecycle: joiners, transfers, suspension, termination / Çalışan yaşam döngüsü](#8-employee-lifecycle-joiners-transfers-suspension-termination--çalışan-yaşam-döngüsü)
9. [Super Admin bootstrap, succession and recovery / Süper Yönetici kurulumu, devri ve kurtarma](#9-super-admin-bootstrap-succession-and-recovery--süper-yönetici-kurulumu-devri-ve-kurtarma)
10. [Quarterly access reviews / Üç aylık erişim kontrolü](#10-quarterly-access-reviews--üç-aylık-erişim-kontrolü)
11. [Audit evidence and the Auditor / Denetim kanıtı ve Denetçi](#11-audit-evidence-and-the-auditor--denetim-kanıtı-ve-denetçi)
12. [Existing access, imports and orphan accounts / Mevcut erişimler, içe aktarma ve sahipsiz hesaplar](#12-existing-access-imports-and-orphan-accounts--mevcut-erişimler-içe-aktarma-ve-sahipsiz-hesaplar)
13. [Exceptions: the daily security worklist / Güvenlik istisnaları](#13-exceptions-the-daily-security-worklist--güvenlik-istisnaları)
14. [Technical architecture and background workers / Teknik mimari ve arka plan işleri](#14-technical-architecture-and-background-workers--teknik-mimari-ve-arka-plan-işleri)
15. [End-to-end scenarios / Uçtan uca senaryolar](#15-end-to-end-scenarios--uçtan-uca-senaryolar)
16. [Implemented, simulated and future / Uygulanan, simüle edilen ve gelecek](#16-implemented-simulated-and-future--uygulanan-simüle-edilen-ve-gelecek)
17. [Practical FAQ / Sık sorulan sorular](#17-practical-faq--sık-sorulan-sorular)
18. [Short interview answers (English)](#18-short-interview-answers-english)

---

## 1. What AccessFlow is / AccessFlow nedir

> **Kısaca (TR):** AccessFlow, bir şirketin iç araçlarına (AWS, GitHub, Jira, bordro portalı…) kimin hangi seviyede erişebildiğini yöneten bir erişim yönetişim uygulamasıdır. Erişim talep edilir, doğru kişiler onaylar, erişim verilir ve doğrulanır, süre dolunca veya kişi ayrılınca kaldırılır. Her adım değiştirilemez bir denetim kaydına yazılır. AccessFlow dış araçların kendisi değildir: API'li araçlarda değişikliği kendisi yapar (demo'da sahte servise), diğerlerinde insanlara görev verir ve sonucun kaydını tutar.

**The problem (CASE.md).** A company of about 400 people runs about 60 internal tools, administered in three different ways. Access was granted by chat messages and spreadsheets; nobody could say who had access to what or who approved it, and 14 former employees still had accounts.

**What AccessFlow does.**

- **One place to request access** to any tool at a chosen level, with a reason and a duration.
- **The right approvers**, per access level: the manager always, plus the Tool Owner and/or Information Security when the level is sensitive.
- **Fulfillment by the tool's real method:** automatically through an API, or as a task for the people who administer the tool by hand or through a vendor.
- **Verification:** API changes are read back from the tool; manual and vendor results are recorded as *self-reported* confirmations by a named admin.
- **Removal:** on expiry, on transfer, on termination, after a review decision, or when someone gives access up.
- **Quarterly reviews**, in which Tool Owners confirm or remove each grant, with reminders and escalation.
- **Evidence:** an append-only audit log, point-in-time "who had access on date X and who approved it", and reports.

**What AccessFlow does not do.** It does not log people into AWS or GitHub, it is not an identity provider, and it cannot see inside tools it has no API for. For Manual and Vendor tools it can only show what an admin *says* was done ([Manual / İnsan işi]).

---

## 2. Concepts and terminology / Kavramlar ve terimler

> **Kısaca (TR):** Aşağıdaki sözlük, arayüzdeki Türkçe adları İngilizce karşılıkları ve anlamlarıyla eşleştirir.

### 2.1 Core objects / Temel nesneler

| English | Türkçe (UI) | Meaning |
|---|---|---|
| Tool | Araç | An internal system, e.g. AWS Console. Has one owner, one admin group and one fulfillment method. |
| Access level | Erişim seviyesi | A named permission inside a tool, e.g. AWS Console *ReadOnly* or *Admin*. The unit of access is **tool + level** (F1). |
| Approval steps | Onay adımları | Per level: Manager (always), Tool Owner (optional), InfoSec (optional) (F2). |
| Request | Talep | One submission; it may hold several items (one per tool). |
| Request item | Talep kalemi | One tool + level for one person, with its own approval chain. |
| Request kinds | Talep türü | New access (Yeni erişim), Level change (Seviye değişikliği), Extension (Süre uzatma), Direct grant (Doğrudan erişim). |
| Grant | Erişim | The resulting access record for one person, tool and level, with its status and history. |
| Operation | İşlem | One change sent to a tool or a task: Grant (Erişim ver), Revoke (Erişimi kaldır), Suspend (Askıya al), Resume (Yeniden etkinleştir). |
| Fulfillment task | Karşılama görevi | A Manual or Vendor operation waiting for an admin-group member. |
| Duration | Süre | **Temporary** (Geçici) with an end date, or **Until Revoked** (Kaldırılana kadar) (D6). |
| Starter package | Başlangıç paketi | Recommended tools for a department or position; a suggestion, never a grant (F3). Demo: Engineering → Jira/User + GitHub/Developer. |
| Admin group | Yönetim grubu | The people who carry out Manual and Vendor tasks for a tool. |
| Tool Owner | Araç sahibi | The person accountable for a tool: approves Tool Owner steps and reviews the tool's access each quarter. |
| Review campaign | Erişim Denetimi | A quarterly campaign, titled "Erişim Denetimi — <year> <n>. Çeyrek". The page is "Erişim Kontrolü". |
| Finding | Bulgu | A review item nobody answered by day 21; stays open for InfoSec. |
| Orphan account | Sahipsiz hesap | An account found in a tool that matches no employee (D10). |
| Exceptions | İstisnalar | The worklist of real problems: failures, orphans, findings, stuck steps. |
| Evidence | Kanıt | How a state was established: **verified** (Doğrulandı, read back from the tool) or **self-reported** (Beyana dayalı, confirmed by an admin); imported access shows **İçe aktarıldı**. |
| Audit event | Denetim olayı | An append-only record of who did what, when and why. |

### 2.2 Statuses / Durumlar

**Grant (Erişim)**

| Status | Türkçe | Meaning |
|---|---|---|
| `PROVISIONING` | Sağlanıyor | API grant queued or being applied by the runner. |
| `PENDING_FULFILLMENT` | Karşılanmayı bekliyor | Manual or Vendor task waiting for an admin. |
| `WAITING_VENDOR` (display only) | Tedarikçi bekleniyor | A Vendor task whose ticket reference is recorded; still `PENDING_FULFILLMENT` underneath. |
| `ACTIVE` | Aktif | Access confirmed (verified or self-reported). Counts as held. |
| `FAILED` | Başarısız | The grant could not be applied; listed in Exceptions. |
| `SUSPENDED` | Askıda | Suspension confirmed in the tool. |
| `REVOCATION_REQUESTED` | Kaldırma talep edildi | Removal on its way. **Still counts as held** (O1). |
| `REVOCATION_FAILED` | Kaldırma başarısız | Removal failed; a security exception. Still held. |
| `REVOKED` | Kaldırıldı | Removal verified or confirmed. |
| `CANCELLED` | İptal edildi | Never became access (e.g. cancelled before it was sent). |

**Request item (Talep kalemi):** Onay bekliyor (pending approval), Onaylandı (approved), Reddedildi (rejected), Geri çekildi (withdrawn), İptal edildi (cancelled).

**Approval step (Onay adımı):** Sırada (waiting for an earlier step), Bekliyor (pending), Onaylandı, Reddedildi, Gönderimle karşılandı (satisfied by a direct manager's submission, D3), Süper Yönetici geçersiz kıldı (Super Admin override, D1), İptal edildi.

**Employee (Çalışan):** Aktif, Askıda (suspended), Ayrıldı (terminated).

### 2.3 Navigation / Menü

| Menu item | English | Who sees it |
|---|---|---|
| Ana sayfa | Home | Everyone except the Auditor |
| Araç kataloğu | Tool catalog (request access here) | Everyone except the Auditor |
| Taleplerim | My Requests | Everyone except the Auditor |
| Erişimlerim | My Access | Everyone except the Auditor |
| E-posta kutusu | Mock Email Outbox | Super Admins; a Super Admin nominee sees only their own role-acceptance e-mail while it waits |
| Onay kutusu | Approval Inbox | Managers, Tool Owners, InfoSec, Super Admins, anyone with a pending step |
| Ekibim | My Team | Managers |
| Erişim Kontrolü | Quarterly reviews | Tool Owners, InfoSec, Super Admins |
| Karşılama görevleri | Fulfillment tasks | Admin-group members |
| Çalışanlar | Employees | HR Managers, Super Admins |
| İstisnalar | Exceptions | InfoSec, Super Admins |
| Araç yönetimi · Doğrudan erişim · Yönetim ayarları | Tools, Direct grant, Settings | Super Admins |
| Denetim kayıtları | Audit records | Super Admins, InfoSec, Managers (their reports), the Auditor |

The menu is convenience only: every permission is enforced by the server (`web/src/nav.ts`, the API's middleware and services).

---

## 3. Roles, permissions and responsibilities / Roller, yetkiler ve sorumluluklar

> **Kısaca (TR):** Dört uygulama rolü vardır: Süper Yönetici, Bilgi Güvenliği, İK Yöneticisi ve Denetçi. Yönetici, araç sahibi ve yönetim grubu üyeliği rol değil, ilişkidir: kimin yöneticisi olduğun, hangi aracın sahibi olduğun, hangi gruba üye olduğun. Kimse kendi erişimini onaylayamaz, karşılayamaz veya gözden geçiremez.

There are four **application roles** (`SUPER_ADMIN`, `INFOSEC`, `HR_MANAGER`, `AUDITOR`). Manager, Tool Owner and admin-group member are **relationships**, not roles: they come from the reporting line, the tool's owner field and the group membership.

| Who / Kim | Can do | Cannot do | Daily responsibility |
|---|---|---|---|
| **Employee / Çalışan** | Request any active tool/level (F3) with a reason and duration; withdraw before provisioning (O5); give own access up without approval (O6); request an extension or level change | Approve, fulfil or review own access | Follow My Requests and My Access |
| **Manager / Yönetici** | Decide Manager steps; request, extend or change access for anyone below them (D3); remove reports' access at any time; see the team's access and audit history (current reports only) | Act on former reports; approve own access | Clear the Approval Inbox |
| **Tool Owner / Araç sahibi** | Decide Tool Owner steps; decide Keep or Remove on the tool's review items | Review own access (it goes to another reviewer) | Approvals; the quarterly review |
| **Admin-group member / Yönetim grubu üyesi** | Claim, record a vendor ticket, confirm or report failure on the tool's tasks; retry a failed operation | Work a task for own grant, or for a direct grant they started | Work the task queue; do the change in the external tool |
| **InfoSec / Bilgi Güvenliği** | Decide InfoSec steps; reassign stuck steps (D9); decide escalated review items after day 14; revoke or dismiss orphans; see Exceptions and company-wide audit; authorize a Super Admin nomination | Approve own access | Check Exceptions daily |
| **HR Manager / İK Yöneticisi** | Create and edit employees; transfer, suspend, reactivate, terminate, schedule termination, rehire; keep the department and position catalog; request on behalf of anyone (D3); suspend, schedule or terminate a Super Admin; nominate a Super Admin successor | Act on themselves; act on an account holding InfoSec, or reactivate or reset a Super Admin (both need a Super Admin) | Record joiners, movers and leavers on the day |
| **Super Admin / Süper Yönetici** | Configure tools, levels, steps, owners, groups; assign roles (two-person rule for Super Admin, D2); imports; start a review campaign; override a pending step (D1); direct grant up to 90 days; record **Remove** (never Keep) on review items; Mock Email Outbox and activation links | Direct-grant to themselves; Keep a review item; confirm a Manual/Vendor task of their own direct action | Configuration; occasional emergency actions (all reported) |
| **Auditor / Denetçi** | Read, filter and CSV-export audit events; point-in-time access; approval evidence | Everything else: every action, every other screen, the evidence reports | Independent, external, read-only (O7) |

**Rules that hold for every role**

- **Separation of duties.** Nobody approves, fulfils or reviews their own access. Each approval step of one item is decided by a different person. A direct-grant initiator cannot confirm the task their grant created.
- **Nobody acts on themselves** in administration: no editing your own record or manager (`self_change_forbidden`), no changing your own roles, no suspending or terminating yourself.
- **The Auditor role overrides every other role.** An account holding `AUDITOR` is treated as an Auditor only. Since 2026-10-31 the API also refuses to give an Auditor any duty (`409 employee_is_auditor`) and refuses `AUDITOR` to someone who has duties (`409 auditor_has_duties`).
- **The Mock Email Outbox (E-posta kutusu) is a Super Admin screen** (O2 note 2026-10-31). A pending Super Admin nominee sees only their own role-acceptance message there; every other role, InfoSec and the Auditor included, is refused (`403`).
- **No delegation** (D9). If an approver is away, InfoSec or a Super Admin reassigns the step to another eligible person, with a reason.

**Demo people / Demo kişileri** (password holders are listed in the [README](../README.md#demo-accounts); the others have no password until a Super Admin issues an activation link)

| Person | Department / title | Roles and relationships |
|---|---|---|
| admin@accessflow.local | Management | Super Admin (the only one) |
| ayse.demir | Human Resources | HR Manager; owner of Payroll Portal; Vendor Desk member |
| selin.aydin | Information Security, Security Analyst | InfoSec |
| emre.kaya | Information Security, Cloud Security Engineer | InfoSec; owner of AWS Console; IT Operations member |
| can.ozturk | IT Operations Lead | Owner of Jira and GitHub; IT Operations member |
| gizem.arslan | Compliance | Auditor |
| burak.sahin | Engineering Manager | Manager of deniz.kara, elif.celik, mert.acar, zeynep.yildiz |
| hakan.koc | Finance Manager | Manager of ece.polat, levent.uysal; Vendor Desk member |
| serkan.tas | Sales Manager | Manager of leyla.ekinci, oya.sezer, tuna.erdem; owner of Salesforce |
| zeynep.yildiz | Engineering, Product Designer | Owner of Figma |

---

## 4. Admin groups, Tool Owners and the Vendor Desk / Yönetim grupları, araç sahipleri ve Tedarikçi Masası

> **Kısaca (TR):** Araç sahibi aracın "sahibidir": kimin erişmesi gerektiğine karar verir ve üç ayda bir erişimleri gözden geçirir. Yönetim grubu ise aracın "elleridir": Manuel ve Tedarikçi araçlarda değişikliği dış sistemde fiilen yapan kişilerdir. Tedarikçi Masası (Vendor Desk), dış firmanın yönettiği araçlar için talep açan ve sonucu kaydeden gruptur. Sahip ile grup ayrı kavramlardır; aynı kişi ikisinde de olabilir ama kendi erişimine dokunamaz.

**Tool Owner — decides.** One active employee per tool. Approves the Tool Owner step on levels that require it and reviews the tool's grants each quarter. When the owner changes, the tool's undecided review items move to the new owner in the same transaction (D8 note 2026-10-31).

**Admin group — does.** A named group of employees created by a Super Admin; every tool points to one group. Only **active members** of that group see and work the tool's Manual and Vendor tasks. A Super Admin who is not a member cannot work a task. Memberships are intervals: removing a member closes the interval, nothing is deleted, and the Privileged roles report (D11) shows who was a member on any date.

**Vendor Desk.** In the demo, the group for tools run by an outside company. Its members (hakan.koc, ayse.demir) do not change anything themselves; they open a ticket with the vendor, record the ticket number in AccessFlow, wait, and confirm when the vendor reports the change done.

**Demo tools / Demo araçları**

| Tool | Method | Owner | Admin group | Levels (extra steps after Manager) |
|---|---|---|---|---|
| Jira | API | can.ozturk | IT Operations | User (none) · Admin (Tool Owner + InfoSec) |
| GitHub | Manual | can.ozturk | IT Operations | Developer (Tool Owner) |
| AWS Console | API | emre.kaya | IT Operations | ReadOnly (Tool Owner) · Admin (Tool Owner + InfoSec) |
| Figma | Manual | zeynep.yildiz | IT Operations | Editor (none) |
| Salesforce | API | serkan.tas | IT Operations | Sales User (Tool Owner) |
| Payroll Portal | Vendor | ayse.demir | Vendor Desk | User (Tool Owner) |

Admin groups: **IT Operations** = can.ozturk, emre.kaya · **Vendor Desk** = hakan.koc, ayse.demir.

**Why separate the two?** The person who decides that access is right (owner) and the person who carries out the change (group) are different controls. Keeping them apart also gives Manual and Vendor tools a queue with a named team instead of one person's inbox.

---

## 5. The three fulfillment methods, step by step / Üç karşılama yöntemi, adım adım

> **Kısaca (TR):** Onaydan sonra erişim, aracın yöntemine göre verilir. **API:** AccessFlow değişikliği kendisi gönderir ve araçtan geri okuyarak doğrular. **Manuel:** yönetim grubundan biri görevi üstlenir, değişikliği dış sistemde elle yapar ve AccessFlow'da onaylar (beyana dayalı). **Tedarikçi:** gruptan biri tedarikçiye talep açar, talep numarasını girer, tedarikçi işi bitirince onaylar (beyana dayalı). Kaldırma, askıya alma ve yeniden etkinleştirme aynı yolu izler.

All three methods share one pipeline: an approved item creates a grant and a `GRANT` operation in the same database transaction as the approval and its audit events. Removals (`REVOKE`), suspensions (`SUSPEND`) and resumes (`RESUME`) use the same pipeline. Only the *executor* differs.

### 5.1 API (e.g. AWS Console, Jira, Salesforce) — [Simulated / Simüle]

| # | AccessFlow does | People do |
|---|---|---|
| 1 | The final approval creates the grant (`PROVISIONING`) and a queued `GRANT` operation. | Nothing. |
| 2 | The **operation runner** claims the operation (`FOR UPDATE SKIP LOCKED`, 30-second lease) and calls the tool with the operation ID (5-second timeout, outside any transaction). | — |
| 3 | It reads the account back from the tool. Only the expected state counts as success. | — |
| 4 | Success: grant `ACTIVE`, evidence **verified** (Doğrulandı), audit event, in the same transaction. | — |
| 5 | Transient failure (timeout, 5xx, bad answer): retried up to **5 attempts** (backoff 30 s, 1 min, 2 min, 4 min). A `409` (the tool refuses) fails at once. | — |
| 6 | After the last attempt: `FAILED`, listed in **İstisnalar**. | InfoSec sees it; a Super Admin or an admin-group member presses retry (a new round of 5 attempts, same operation ID). |

In the demo the "tool" is the **Mock Tool Service**, a small separate service with its own database that stores (tool, account, level). The account is the employee's corporate e-mail. In a real deployment an adapter implementing `access.ToolClient` (`Apply`, `Account`) would call the real AWS or Jira API ([Future / Gelecek]); approval, audit and lifecycle logic would not change.

**Optional GitHub sandbox adapter — [Implemented, off by default; not production-ready].** One extra API tool can be served by real GitHub instead of the mock: Read on one private sandbox repository ([README](../README.md#optional-github-sandbox-adapter), DECISIONS.md A1 amendment 2026-11-01). Such a tool is created with the account identity **GitHub kullanıcı adı**, so its account is not the e-mail:

| # | AccessFlow does | People do |
|---|---|---|
| 1 | The request form (or the Super Admin's direct grant) requires **GitHub kullanıcı adı**. The server checks it with GitHub and stores the login as GitHub spells it, with GitHub's numeric user id. | The requester types their GitHub username. |
| 2 | Approvers see **GitHub hesabı: @login · GitHub kimliği …** before deciding; the audit trail records it. It cannot be changed after submission; a level change or extension keeps it. | The Manager and Tool Owner check the account belongs to the person. |
| 3 | After approval GitHub sends an invitation; the grant stays `PROVISIONING` until it is accepted and Read is verified. | The person accepts the invitation in GitHub. |
| 3b | A failure (the account already has access or an invitation, the invitation is declined or expires, GitHub refuses a call) leaves the grant failed; **İstisnalar** explains it in Turkish and keeps only GitHub's status code as the technical detail. Username problems (missing, invalid, not found, GitHub unavailable or rate-limited) are shown next to the field before anything is stored. | An admin resolves it in GitHub and retries from İstisnalar. |
| 4 | Every removal, suspension and read-back uses the stored login and id. A renamed or deleted account is never acted on: the operation fails into **İstisnalar** with the reason. | InfoSec or an admin checks the repository and resolves it there. |

The tool catalog card (every employee) and the tool's page (Super Admin) show which service runs each API tool: **Gerçek GitHub API entegrasyonu aktif** for the tool the adapter serves, **Mock Tool Service kullanılıyor** for every other. The invitation, acceptance, Read verification and revocation were live-tested on a sandbox repository (2026-11-01). The demo's own "GitHub" tool is a Manual tool (§5.2) and is not affected.

### 5.2 Manual (e.g. GitHub, Figma) — [Implemented] + [Manual / İnsan işi]

| # | AccessFlow does | People do (in the external tool) |
|---|---|---|
| 1 | The final approval creates the grant (`PENDING_FULFILLMENT`) and a task in the tool's admin group queue (**Karşılama görevleri**). | — |
| 2 | Shows the task to every active group member, with the person, tool, level, request and waiting time. Leavers' removals are listed first. | A member opens the queue. |
| 3 | **Claim (Üstlen):** the task is now theirs (`409 task_claimed` for anyone else). | — |
| 4 | — | The member signs in to the tool's own admin console (e.g. GitHub organization settings) and adds the person's account at the right level (e.g. invites them to the team with Developer permissions). |
| 5 | **Confirm (Tamamlandı):** grant `ACTIVE`, evidence **self-reported** (Beyana dayalı) with the confirming admin and time. | — |
| 5′ | **Fail (Başarısız) with a reason:** grant `FAILED`, task goes to **İstisnalar**. | Fix the cause (e.g. buy a licence), then a member or Super Admin retries; the task returns to the queue unclaimed. |

Who may work the task: an active member of the tool's group, **never the grant's employee and never the Super Admin who started a direct grant** (`403 separation_of_duties`). Only the claimer may confirm or fail (`403 not_claimer`). No timers chase Manual tasks; they wait in the queue as normal work (O4).

### 5.3 Vendor (e.g. Payroll Portal) — [Implemented] + [Manual / İnsan işi]

| # | AccessFlow does | People do |
|---|---|---|
| 1 | Creates the grant (`PENDING_FULFILLMENT`) and a task in the Vendor Desk queue. | — |
| 2 | **Claim.** | A Vendor Desk member takes the task. |
| 3 | — | They open a ticket with the vendor in the vendor's own portal or by e-mail ("please create a User account for ece.polat@…"). |
| 4 | **Record ticket (Talep numarası):** stores the reference (≤ 100 characters); the grant shows **Tedarikçi bekleniyor** (`WAITING_VENDOR`). | — |
| 5 | — | The vendor does the work and reports back. |
| 6 | **Confirm:** `ACTIVE`, self-reported, with the ticket reference. A Vendor task cannot be confirmed without a ticket (`409 ticket_required`). | — |

The difference from Manual: the person who confirms did not make the change themselves; they rely on the vendor's answer. The ticket reference is the link to the vendor's own record.

### 5.4 Why "verified" and "self-reported" are kept apart

An API result is observed: AccessFlow read it from the tool. A Manual or Vendor result is a statement by a named person. Every screen, report and point-in-time query shows which one it is (O1), so an auditor can weigh the evidence. AccessFlow cannot detect a Manual removal that was confirmed but not actually done; the quarterly review and the tool's own exports are the check for that.

---

## 6. Demo walkthroughs: AWS Console, GitHub, Payroll Portal / Demo örnekleri

> **Kısaca (TR):** Üç araç, üç yöntemi temsil eder. AWS Console (API): Mert'in Admin talebi yöneticide bekliyor, Tuna'nın ReadOnly talebi reddedildi. GitHub (Manuel): Deniz'in erişimini Can elle verdi ve onayladı; Mert'in görevini Emre lisans sınırı yüzünden başarısız bildirdi. Payroll Portal (Tedarikçi): Ece'nin talebi için Hakan tedarikçiye TDR-2026-0142 numaralı talebi açtı; tedarikçi bekleniyor. Bunların hepsi temiz bir başlangıçta hazır gelir.

> AWS, GitHub and the other names are **demo catalog entries**. No third-party system is integrated.

### 6.1 AWS Console — API

**Setup.** Owner emre.kaya (also InfoSec). Admin group IT Operations. *ReadOnly* needs Manager + Tool Owner; *Admin* needs Manager + Tool Owner + InfoSec.

**Seeded state**

- **mert.acar → AWS Console/Admin**, reason "Üretim ortamı olay müdahalesi", waiting at the **Manager step** (burak.sahin).
- **tuna.erdem → AWS Console/ReadOnly**, **rejected** by the manager serkan.tas: "Satış raporları için AWS erişimi gerekmiyor; BI panosunu kullanın." A rejection is final; a new request would start again (O5).

**Continue it yourself (Mert's Admin request)**

1. burak.sahin opens **Onay kutusu** and approves the Manager step. He sees the reason, the duration and how long it has waited.
2. emre.kaya approves the Tool Owner step.
3. The InfoSec step cannot go to emre: he already decided a step of this item. It is decided by selin.aydin.
4. On the final approval the grant is `PROVISIONING`; within seconds the runner sends `GRANT` to the Mock Tool Service, reads the account back and marks it `ACTIVE` / **verified**.
5. Mert sees it in **Erişimlerim**. If it was Temporary, it is removed automatically from 00:00 after the end date (Europe/Istanbul).

**What people do in the real AWS:** in this design, nothing for an API tool; the adapter would make the change (for example, assign a permission set in the identity tooling). That adapter is [Future]. Today the change exists only in the Mock Tool Service.

### 6.2 GitHub — Manual

**Setup.** Owner can.ozturk. Group IT Operations (can.ozturk, emre.kaya). *Developer* needs Manager + Tool Owner.

**Seeded state**

- **deniz.kara → GitHub/Developer** (with Jira/User, from the Engineering starter package), approved by burak.sahin (Manager) and can.ozturk (Tool Owner). can.ozturk claimed and **confirmed** the task → `ACTIVE`, self-reported. (Jira/User, an API tool, was granted and verified by the runner.)
- **mert.acar → GitHub/Developer**, approved; emre.kaya claimed the task and **reported failure**: "GitHub organizasyonundaki lisans sınırı doldu" → `FAILED`, shown in **İstisnalar**.
- **elif.celik → GitHub/Developer**, Manager approved, waiting at the **Tool Owner** step (can.ozturk).

**What people do in the real GitHub.** An IT Operations member signs in to the GitHub organization as an owner, invites the person's account or adds it to the right team with the right role, and only then presses **Tamamlandı** in AccessFlow. For a removal they remove the member from the organization and confirm the removal task.

**Recovering Mert's failure.** Someone frees or buys a seat in GitHub. Then an IT Operations member (not Mert) or a Super Admin presses retry in İstisnalar; the task goes back to the queue unclaimed, a member claims it, adds Mert in GitHub and confirms.

### 6.3 Payroll Portal — Vendor

**Setup.** Owner ayse.demir. Group Vendor Desk (hakan.koc, ayse.demir). *User* needs Manager + Tool Owner.

**Seeded state**

- **ece.polat → Payroll Portal/User**, reason "Aylık bordro kontrolü". Approved by hakan.koc (her manager) and ayse.demir (Tool Owner). hakan.koc, a Vendor Desk member, claimed the task and recorded vendor ticket **TDR-2026-0142** → shows **Tedarikçi bekleniyor**.

Being Ece's manager and approver does not stop Hakan from fulfilling her task: only the beneficiary and a direct-grant initiator are excluded.

**What people do outside AccessFlow.** Hakan sends the request to the payroll vendor (their portal or e-mail) and gets ticket TDR-2026-0142. When the vendor replies that the account exists, Hakan opens the task and presses **Tamamlandı**. The grant becomes `ACTIVE`, self-reported, with the ticket reference on the evidence.

---

## 7. The access lifecycle / Erişim yaşam döngüsü

> **Kısaca (TR):** Talep → onay → sağlama → doğrulama → kullanım → kaldırma. Her talep kalemi kendi onay zincirinden geçer; onaylanınca erişim aracın yöntemine göre verilir; erişim yalnızca doğrulandığında veya onaylandığında "aktif" sayılır ve yalnızca kaldırma doğrulandığında veya onaylandığında biter.

### 7.1 Request / Talep — [Implemented]

1. The employee opens **Araç kataloğu**, chooses one or more tools and a level for each (a starter package can pre-fill them), writes a justification and chooses the duration: Temporary (quick picks 7/30/90 days or a date) or Until Revoked.
2. **On behalf (D3):** only a manager above the beneficiary, an HR Manager or a Super Admin may request for someone else. A **direct manager's** submission counts as the Manager step ("Gönderimle karşılandı"), unless that manager is an HR Manager or Super Admin.
3. **One level per tool (D4):** a person holds at most one level of a tool. Asking for another level is a **level change**; when approved, the new level is granted first and the old one removed afterwards.
4. **Extension (D6):** a later date or Until Revoked on a temporary active grant goes through the level's approval steps again; it changes only the date, with no tool call.
5. **Mock e-mail (O2):** a new-access submission needing the Manager's approval records one simulated e-mail addressed to the manager in the Mock Email Outbox, which only Super Admins can open. It only informs; nothing is approved by e-mail. [Simulated]

### 7.2 Approval / Onay — [Implemented]

- Steps run in order: Manager → Tool Owner (if required) → InfoSec (if required), each by a different person.
- If an assignee is unavailable (suspended, terminated or excluded by separation of duties), the step is routed to a fallback (F2); a step with nobody eligible appears in İstisnalar.
- **Reject** needs a reason and ends the item. **Withdraw** is possible until provisioning starts.
- **Reassign (D9):** InfoSec or a Super Admin moves a stuck step to another eligible approver, with a reason.
- **Override (D1):** a Super Admin may approve any pending step with a mandatory reason. It is recorded as `SUPER_ADMIN_OVERRIDE` with the Super Admin's identity, never as the original approver's decision, and appears in the overrides report.
- Concurrent decisions: the first wins; later attempts are refused.
- An item whose temporary end date passed while it waited is cancelled at final approval.

### 7.3 Provisioning and verification / Sağlama ve doğrulama

As in section 5. Effective access (O1) starts when activation is **verified** (API) or **confirmed** (Manual/Vendor), at the time AccessFlow recorded it; no earlier date is ever entered.

### 7.4 Direct grant / Doğrudan erişim — [Implemented]

A Super Admin may grant access directly, skipping the approval steps (D1). It is always temporary, **at most 90 days** (latest date: today + 89), never for the Super Admin's own access, uses the tool's normal method, and is reported. InfoSec, the beneficiary's manager and any bypassed approver are notified in the app. Demo: admin gave **elif.celik Figma/Editor for 14 days**, "Lansman öncesi acil tasarım düzeltmesi"; the Figma task waits for IT Operations, and the admin could not confirm it even if they were a member.
For a tool whose accounts are GitHub usernames (optional sandbox adapter, §5.1), the direct grant also requires the person's GitHub username, checked and recorded exactly as on a request.

### 7.5 Revocation / Kaldırma — [Implemented]

Triggers: the employee gives access up (O6), the manager removes a report's access, a temporary end date passes (reason `EXPIRED`), a transfer, a termination, a review **Remove** decision, an orphan revoke, or a Super Admin revoke.

1. The grant becomes `REVOCATION_REQUESTED` (still counts as held).
2. API: the runner sends `REVOKE` and verifies the account is gone. Manual/Vendor: a removal task for the admin group; a member removes the account in the tool (or asks the vendor) and confirms.
3. `REVOKED` only after verification or confirmation. A failure is `REVOCATION_FAILED`, a security exception in İstisnalar.

Demo: levent.uysal's Figma access (Manual) is `REVOCATION_REQUESTED` after his termination, waiting for IT Operations to remove him in Figma and confirm.

---

## 8. Employee lifecycle: joiners, transfers, suspension, termination / Çalışan yaşam döngüsü

> **Kısaca (TR):** İK işe girişleri, ekip değişikliklerini, askıya almayı ve işten çıkışı kayda geçirir; erişim değişiklikleri buna göre otomatik başlar. İşe giren çalışan için erişim talebi açılır (başlangıç paketi önerir). Ekip değişikliğinde tüm erişimler kaldırılmak üzere seçili gelir. Askıya alma geri alınabilir. İşten çıkış her şeyi kaldırır; yeniden işe alım hiçbir şeyi geri getirmez.

Who acts: HR Managers and Super Admins. An account holding InfoSec needs a Super Admin for every lifecycle action; a Super Admin may be suspended, scheduled or terminated by HR, but only a Super Admin reactivates them or resets their password. Nobody acts on themselves.

### 8.1 Joiner / İşe giriş — [Implemented]

1. HR creates the employee in **Çalışanlar** (department and position from the company catalog, a manager), or a Super Admin imports them with the onboarding workbook.
2. The employee gets **no password**: an **activation link** (single use, 24 hours) is placed in the Mock Email Outbox. A Super Admin reveals it (audited) and passes it on; the employee sets a password on "Şifreni Belirle". [Simulated delivery]
3. Access: the employee, or the manager on their behalf, requests the starter package. The manager's submission satisfies the Manager step.

### 8.2 Transfer (mover) / Ekip değişikliği — [Implemented]

1. HR chooses the new department and/or manager. AccessFlow lists every open grant **pre-selected for removal**, with "Revoke All" / "Keep All" shortcuts (D5).
2. Keeping an item needs a written justification; kept items are marked "retained on transfer", the new manager gets one notification listing them, and they are flagged in the next review.
3. Removed items go through the normal removal. Pending requests: a pending Manager step moves to the new manager; a request the old manager already approved is cancelled ("transferred"). If HR removed everything, pending requests are cancelled.
4. A job-title-only change is just an audit event.
5. A grant that activates while its transfer removal is open is a **stale grant incident**; the removal still runs.

### 8.3 Suspension / Askıya alma — [Implemented]

Example: HR suspended **oya.sezer**, "Uzun süreli izin".

1. Login disabled at once, sessions ended. Pending requests cancelled; her approvals, reviews and claimed tasks released or reassigned.
2. Each active grant gets a `SUSPEND`: verified for API tools (her Salesforce is `SUSPENDED`), a disable task for Manual/Vendor tools. A grant shows suspended only once confirmed.
3. Expiry, reviews and removals continue during suspension.
4. **Reactivation:** each grant is re-evaluated; expired, revoked or retired ones are not restored, the rest get `RESUME` through the normal pipeline.

**People in external tools:** for Manual/Vendor tools the admin disables (not deletes) the account, then later re-enables it.

### 8.4 Termination / İşten çıkış — [Implemented]

Example: HR terminated **levent.uysal**, "İstifa"; scheduled **tuna.erdem**'s termination in 21 days, "Sözleşme sonu".

1. **Immediate:** login disabled, sessions ended, pending requests cancelled, tasks released, approvals and reviews reassigned, roles and group memberships closed.
2. **All access removed** through the normal workflow with the highest priority (leavers first in the runner and in task queues). Removals stay visible in İstisnalar ("Sahipsiz ve Şüpheli Hesaplar") until verified or confirmed.
3. **Scheduled:** the sweep terminates at the effective time, catching up after downtime. HR can reschedule or cancel. Immediate termination overrides a schedule.
4. **Rehire** is an explicit process: a new activation link and fresh requests. **Nothing is restored**: no grant, role, membership or request.

This is the direct answer to CASE.md's "14 former employees still had accounts": every leaver's access is listed until someone proves it is gone.

---

## 9. Super Admin bootstrap, succession and recovery / Süper Yönetici kurulumu, devri ve kurtarma

> **Kısaca (TR):** İlk Süper Yönetici kurulumda bir kez, tek kullanımlık şifreyle oluşturulur. Yeni Süper Yönetici iki kişiyle atanır. Son aktif Süper Yönetici işten çıkarılamaz; önce bir halef atanmalıdır. İK acil durumda onu askıya alabilir. Halef süreci: İK aday gösterir, bağımsız bir Bilgi Güvenliği üyesi veya Süper Yönetici yetkilendirir, aday 24 saatlik bağlantıyla kendi hesabından kabul eder. Hiç aktif Süper Yönetici kalmazsa son çare sunucu komutudur.

### 9.1 Bootstrap / İlk kurulum — [Implemented]

- On first start, if no Super Admin has **ever** existed, the app creates one ("Initial Super Admin", e-mail from `BOOTSTRAP_ADMIN_EMAIL`) under an advisory lock.
- Its random one-time password is written to a file readable only by the app (mode 0600) on the app data volume, read with `docker compose exec app /accessflow bootstrap-password`. It is never logged. The first sign-in forces a password change.
- In demo mode the demo accounts use the local demo passwords instead ([README](../README.md#demo-accounts), [docs/deployment.md](deployment.md)).

### 9.2 Promotion / Atama — [Implemented]

Two people (D2): a Super Admin nominates, a **different** Super Admin authorizes; with only one Super Admin, a different **InfoSec** member authorizes. The nominee never authorizes. A health warning shows while fewer than two Super Admins are active (the demo has one, so the warning is visible).

### 9.3 The last Super Admin / Son Süper Yönetici — [Implemented]

- **Termination is guarded:** terminating or scheduling the termination of the last active Super Admin returns `409 last_super_admin`. A due scheduled termination waits in the sweep.
- **Emergency suspension is allowed:** an HR Manager may suspend them at once. That leaves no active Super Admin: the app records `ADMIN_RECOVERY_REQUIRED` and notifies HR and InfoSec. HR cannot reactivate a Super Admin (`403 privileged_reset_requires_super_admin`).

### 9.4 Succession / Halef atama — [Implemented, Simulated link delivery]

Open while at most one Super Admin is **not terminated** (active or suspended).

1. An HR Manager (not a Super Admin) nominates an active employee other than themself, from the succession panel ("Yeni Süper Yönetici ata") shown to HR Managers in the recovery warning and on the employee page. Only one succession nomination at a time (`409 nomination_pending`).
2. An **independent** InfoSec member or active Super Admin (neither the proposer nor the nominee) authorizes or declines from the Approval Inbox. All eligible InfoSec members and Super Admins are notified.
3. Only then the nominee gets a **single-use link valid 24 hours** in their own Mock Email Outbox.
4. The nominee accepts while signed in to their own account. They become Super Admin; the outgoing Super Admin's termination can now go through.

Covered by `TestHRSuccessionOfTheLastSuperAdmin`, `TestScheduledTerminationOfTheLastSuperAdminWaits`, `TestSuccessionOnlyWhileTheLastSuperAdminLeaves`.

### 9.5 Last-resort recovery / Son çare — [Implemented]

`recover-admin` (a server command, see [docs/deployment.md](deployment.md)) works **only while no Super Admin is active** and is audited (`TestRecoverAdminOnlyWithoutAnActiveSuperAdmin`). It needs shell access to the server, which is itself a control.

**Demo example (it changes the demo's Super Admin; try it only on a demo you can reset):** the demo has one Super Admin, so succession is already open. ayse.demir (HR) nominates burak.sahin → selin.aydin (InfoSec) authorizes in her Onay kutusu → burak.sahin opens the role-acceptance link ("Rol kabul bağlantısı") in his own E-posta kutusu while signed in as himself and accepts. Now there are two Super Admins and admin could be terminated normally.

---

## 10. Quarterly access reviews / Üç aylık erişim kontrolü

> **Kısaca (TR):** Her çeyrekte bir kampanya başlar ve tüm aktif ve askıdaki erişimleri kapsar. Araç sahibi her kalem için "Erişim devam etsin" veya "Erişimi kaldır" der. 7. gün hatırlatma, 14. gün Bilgi Güvenliğine yükseltme, 21. gün cevapsız kalanlar bulgu olur. **Erişim hiçbir zaman otomatik kaldırılmaz.**

**Process (D8, with the 2026-10-30 note)** — [Implemented]

1. A campaign starts at each calendar quarter (the sweep), or a Super Admin starts one manually. It snapshots every `ACTIVE` and `SUSPENDED` grant (except those with an open finding) and notifies each reviewer once.
2. **Reviewer = the Tool Owner.** The owner's own access, and an InfoSec member's own access, go to a different reviewer (the InfoSec pool).
3. Decisions: **Erişim devam etsin** (Keep) or **Erişimi kaldır** (Remove, reason required, normal removal workflow). Keep does not extend expiry. A terminated employee's access cannot be kept (`409 terminated_access_not_kept`).
4. **Bulk Keep** for plain items; items needing individual attention are skipped: InfoSec levels, retained on transfer, override or direct grant, suspended, imported baseline.
5. **Day 7:** one in-app reminder per unanswered item. **Day 14:** unanswered items escalate to InfoSec, who may decide them. **Day 21:** every unanswered item becomes an **open finding** in İstisnalar; nothing is removed.
6. A Super Admin may record **Remove** but never **Keep**: the role that can grant alone must not also certify.
7. Findings close when decided, or automatically when the grant ends. Later campaigns list carried-over findings in their evidence.

**Demo:** campaign "Erişim Denetimi — 2026 4. Çeyrek" is open; can.ozturk already decided **Keep** on deniz.kara's Jira item.

**What people do outside AccessFlow:** a careful owner checks the tool itself (e.g. GitHub's member list) to see that the list in AccessFlow matches reality, especially for Manual tools.

---

## 11. Audit evidence and the Auditor / Denetim kanıtı ve Denetçi

> **Kısaca (TR):** Her durum değişikliği ve onun denetim olayı aynı veritabanı işleminde yazılır; denetim kaydı yalnızca eklenebilir, veritabanı tetikleyicisi değişiklik ve silmeyi reddeder. "Denetim kayıtları" ekranı olay geçmişini, belirli bir tarihte kimin erişimi olduğunu, onay kanıtını ve kanıt raporlarını gösterir. Denetçi şirket dışından, salt okunur bir roldür.

### 11.1 What is recorded — [Implemented]

- Every state change and its audit event are written **in one transaction**; there is no change without its event.
- The audit table is **append-only**: the app's database user is granted only `SELECT` and `INSERT` on it, and a trigger refuses `UPDATE` and `DELETE` as well (`internal/platform/migrations/00001_audit_events.sql`).
- Each event: actor, action (e.g. `REQUEST_SUBMITTED`, `STEP_APPROVED`, `TASK_CLAIMED`, `GRANT_VERIFIED_BY_TOOL`, `EMPLOYEE_TRANSFERRED`, `SUPER_ADMIN_OVERRIDE`), entity, time and details such as reasons and evidence. Secrets and tokens are never recorded.
- Employees and business records are never physically deleted; history is kept as intervals (roles, memberships, suspensions).

### 11.2 Denetim kayıtları tabs

| Tab | English | Shows |
|---|---|---|
| Olay geçmişi | Event history | Who acted, whose access, the tool and level (**Araç ve seviye**, with the external account when there is one), what happened in Turkish (e.g. *Çalışan kendi isteğiyle vazgeçti*, *Araçta doğrulandı*) and the reason. Tool and level names are the ones in use when the event happened, even after a rename or retirement. Clicking a row opens the event in full, with the stored record (entity, id, raw details) under **Teknik kayıt**. Filter by date, actor, action, tool, text; page through every match; export all matches as CSV with `tool`, `access_level` and `account` columns (streamed by the server). |
| Geçmiş erişim sorgusu | Point-in-time access | Who held access to a tool at an instant or end of a date, each grant's state and evidence (verified / self-reported / imported) and its approvers, plus removals in the previous 30 days. |
| Onay kanıtı | Approval evidence | Each request item's approval chain: who decided each step, when, how (decision, override, submission). |
| Kanıt raporları | Evidence reports | Privileged roles (D11), Super Admin overrides and direct actions, approval policy, leavers, and the quarterly evidence package. **Not shown to the Auditor.** |

**Who sees what (O7):** Super Admin and InfoSec company-wide; a Manager for current direct and indirect reports only; the **Auditor** company-wide but only the first three tabs; everyone else nothing beyond My Requests and My Access.

### 11.3 The Auditor / Denetçi — [Implemented]

- External and independent; holds no duty (no approvals, tasks, reviews, ownership or reports).
- Exact allowlist of five `GET` routes: audit events, events export, filter choices, `/audit/access`, `/audit/requests`. Every other endpoint returns `403 forbidden` (default deny in the session middleware), and the services refuse the reports to the Auditor themselves as defence in depth.
- Demo: **gizem.arslan**. Typical question she can answer alone: "Who had AWS Console Admin on 30 September, and who approved it?" → Geçmiş erişim sorgusu for that date, then the approvers shown on the grant or in Onay kanıtı.

---

## 12. Existing access, imports and orphan accounts / Mevcut erişimler, içe aktarma ve sahipsiz hesaplar

> **Kısaca (TR):** AccessFlow'dan önce var olan erişimler "içe aktarılmış başlangıç erişimi" olarak alınır: API araçları sahte servisten okunur, Manuel ve Tedarikçi araçları CSV ile gelir; ayrıca tek dosyalık bir başlangıç çalışma kitabı vardır. Hesaplar kurumsal e-postayla eşleştirilir. Eşleşmeyen hesap "sahipsiz hesap" bulgusu olur; Bilgi Güvenliği kaldırır veya gerekçeyle kapatır. İçe aktarılan erişimin onaylayanı bilinmez; ilk kontrol kampanyası ilk kararı kaydeder.

### 12.1 Import sources (Super Admin only, D10) — [Implemented]

| Source | Route / screen | For |
|---|---|---|
| Tool read | `POST /imports/mock` | API tools: reads every account the Mock Tool Service holds [Simulated] |
| CSV | `POST /imports/csv` | Manual and Vendor tools only; refused for API tools (`api_tool`) |
| Onboarding workbook (.xlsx) | Yönetim ayarları | Employees, Tools and Existing Access in one file, previewed, applied all or nothing |

Rules: accounts match employees by corporate e-mail, case-insensitive; imports are idempotent; imported grants are `IMPORTED_BASELINE`, Until Revoked, marked "existed before AccessFlow; approver unknown", with source, time and importer. No approval or activation date is invented. An account of a **terminated** employee is recorded and its removal starts immediately. Workbook rows for API tools are **reported** until the tool read confirms them (**verified by the tool**).

### 12.2 Orphan accounts / Sahipsiz hesaplar — [Implemented]

An account matching no employee becomes an `ORPHAN_ACCOUNT` finding, never auto-assigned. InfoSec, in İstisnalar → "Sahipsiz ve Şüpheli Hesaplar", either:

- **revokes** it (reason required; normal removal workflow), or
- **dismisses** it with a reason (e.g. a known service account).

**Demo:** before the baseline import, legacy accounts were placed in the Mock Tool Service:

- elif.celik Jira/User → **matched**, imported as baseline access;
- eski.stajyer@accessflow.local Jira/User → **orphan** (a company-domain account with no employee: likely a former intern);
- ajans@partner-reklam.com Salesforce/Sales User → **orphan** (an outside domain: likely an agency).

**What people do outside AccessFlow:** for Manual/Vendor tools, someone exports the user list from the tool's admin console (or asks the vendor) and turns it into the CSV. For an orphan, InfoSec may ask the tool owner who the account belongs to before deciding.

---

## 13. Exceptions: the daily security worklist / Güvenlik istisnaları

> **Kısaca (TR):** İstisnalar yalnızca gerçek sorunları gösterir; Bilgi Güvenliği her gün kontrol eder.

Shown to InfoSec and Super Admins:

- `FAILED` operations (API or task) with attempts and the last error — demo: Mert's GitHub task;
- unresolved security-critical removals, such as a leaver's access not yet removed — demo: Levent's Figma;
- orphan accounts and departed employees' remaining access ("Sahipsiz ve Şüpheli Hesaplar");
- stale or overlapping states (a D4 level-change overlap, a D5 stale grant);
- approval steps with no eligible approver;
- open review findings.

Pending Manual and Vendor tasks are **not** exceptions; they are normal work in the task queue.

---

## 14. Technical architecture and background workers / Teknik mimari ve arka plan işleri

> **Kısaca (TR):** Go ile yazılmış modüler bir monolit, PostgreSQL veritabanı, React/TypeScript arayüz ve ayrı bir sahte araç servisi. `docker compose up` ile kurulumsuz çalışır, yalnızca 8080 portu açılır. İki arka plan döngüsü vardır: işlem yürütücüsü ve dakikalık taramalar. Kuyruk olarak PostgreSQL kullanılır; ek altyapı yoktur.

### 14.1 Components (A1, A2) — [Implemented]

```
Browser ──HTTPS(in production)/HTTP──► app :8080
                                        ├─ Go API (chi), sessions (JWT in HttpOnly cookie)
                                        ├─ React UI (same origin)
                                        ├─ operation runner  ─HTTP─► mocktools (internal network only)
                                        └─ sweeps (≈1/min)                └─ own "mocktools" database
                                        │
                                        ▼
                                   postgres: "accessflow" database (+ "mocktools" database, separate user)
```

- **app:** one Go binary: the REST API (`/api/v1`), the compiled React UI, both background loops. Swagger UI at `/swagger/` in demo mode (read-only docs).
- **postgres:** PostgreSQL 18, two databases with separate users. Not published to the host.
- **mocktools:** the Mock Tool Service. AccessFlow reaches it **only over HTTP**, never its database.
- **Code layout (A8):** `internal/<domain>` packages — `auth`, `people`, `catalog`, `requests`, `access`, `reviews`, `audit`, `explorer`, `onboarding`, `notify`, `sweeps`, `mocktools`, `demo`, `platform`. Explicit, parameterized SQL (pgx); no ORM, no repository interfaces.
- **Business rules** are pure functions with explicit time parameters, tested table-driven; database behaviour (transactions, the audit trigger, privileges, concurrency) is tested against real PostgreSQL.

### 14.2 Background worker 1: the operation runner (A6)

- The `operations` table **is** the queue. A worker claims due rows with `FOR UPDATE SKIP LOCKED` and takes a **30-second lease** with a fresh token.
- **Priority:** 0 = a terminated employee's removals; 1 = other removals and every `SUSPEND`; 2 = grants and `RESUME`; oldest first.
- The tool call runs **outside** any transaction, carries the operation ID (idempotent at the tool), and has a 5-second timeout. Then the account is read back.
- The result is written only if the lease token still matches, so a crashed or stale worker cannot overwrite a newer result.
- Retries: 5 attempts, backoff 30 s → 1 min → 2 min → 4 min, then `FAILED`.
- Manual and Vendor tasks are **not** runner work; they wait for people.

### 14.3 Background worker 2: the sweeps (A6)

About once a minute, under a PostgreSQL advisory lock (one runs at a time), idempotent, catching up after downtime:

- **expiry:** open temporary grants whose end date is before today (Europe/Istanbul) get a removal with reason `EXPIRED`;
- **scheduled terminations** that are due (except the last Super Admin, which waits);
- **reviews:** start the quarter's campaign; day-7 reminders, day-14 escalation, day-21 findings.

There are no other timers: no expiry reminders, no task chasing, no e-mail reminders (O8).

### 14.4 Why PostgreSQL as the queue

Approval, grant, operation and audit events commit together, so a grant can never exist without its operation. At ~400 employees and 60 tools the queue sees hundreds of operations a day. A broker (Redis, RabbitMQ) would be another service and another source of truth, for no capability the case needs. It becomes justified at many thousands of operations per minute or with several consuming systems.

### 14.5 Security controls (summary)

Passwords hashed; activation and succession tokens stored only as SHA-256; sessions as JWT in an HttpOnly cookie with `token_version` to end all sessions; login rate limiting and password policy; default-deny for the Auditor; every permission enforced on the server; secrets from the environment or generated at first start. HTTPS, backups and secret management are the hosting team's job ([docs/deployment.md](deployment.md)).

---

## 15. End-to-end scenarios / Uçtan uca senaryolar

> **Kısaca (TR):** Aşağıdaki senaryolar demo üzerinde baştan sona izlenebilir. Her adımda kimin oturum açtığı ve dış sistemde ne yapıldığı yazılıdır.

### Scenario A — A new engineer's first day / Yeni mühendisin ilk günü

1. **ayse.demir (HR)** creates "Ali" in Engineering, manager burak.sahin. An activation e-mail lands in the outbox.
2. **admin** reveals the link in E-posta kutusu (audited) and gives it to Ali, who sets a password on "Şifreni Belirle".
3. **burak.sahin** requests the Engineering starter package for Ali: Jira/User and GitHub/Developer, Until Revoked. As direct manager his submission satisfies the Manager step.
4. Jira/User needs nothing more → the runner grants it in the mock tool → `ACTIVE`, verified.
5. GitHub/Developer needs the Tool Owner → **can.ozturk** approves → a task appears for IT Operations.
6. **emre.kaya** claims the task, *adds Ali to the GitHub organization by hand*, presses Tamamlandı → `ACTIVE`, self-reported.
7. Ali sees both in Erişimlerim; the audit log shows every step.

### Scenario B — Privileged AWS access with expiry / Süreli ayrıcalıklı AWS erişimi

1. **mert.acar**'s AWS Console/Admin request (seeded) waits at burak.sahin. (For a time-boxed variant, Mert would choose Temporary, 7 days.)
2. burak → emre (Tool Owner) → selin (InfoSec) approve. The runner grants and verifies it.
3. If Mert chose Temporary, at 00:00 Istanbul time on the day after the end date the sweep requests removal (`EXPIRED`) and the runner revokes and verifies it. There is no expiry reminder; if he needs longer, he requests an extension before then, which re-runs all three steps.

### Scenario C — A vendor-run tool / Tedarikçi aracı

1. Ece's Payroll Portal ticket TDR-2026-0142 is recorded (seeded).
2. *The vendor creates the account in their system.*
3. **hakan.koc** confirms → `ACTIVE`, self-reported, ticket on the evidence.
4. Later, a review **Remove** or Ece's termination creates a removal task; Vendor Desk opens a new vendor ticket, records it, confirms when the vendor deletes the account.

### Scenario D — A team change / Ekip değişikliği

1. **ayse.demir** transfers deniz.kara from Engineering to Sales (manager serkan.tas).
2. Deniz's Jira and GitHub grants are pre-selected for removal. HR keeps Jira with a justification ("Sales still files bugs"), removes GitHub.
3. Jira is retained on transfer; serkan is notified. GitHub gets a removal task: an IT Operations member *removes Deniz from the GitHub organization*, confirms.
4. In the next review, the Jira item is flagged and must be decided individually.

### Scenario E — Leaver with legacy access / Eski erişimi olan ayrılan çalışan

1. **ayse.demir** terminates an employee immediately. Login and sessions end at once.
2. Every grant gets a priority-0 removal: API ones are removed and verified within seconds; Manual/Vendor ones appear first in their groups' queues.
3. Until each is confirmed, **selin.aydin** sees them in İstisnalar under "Sahipsiz ve Şüpheli Hesaplar".
4. *IT Operations removes the accounts from GitHub and Figma*; Vendor Desk asks the payroll vendor. Each confirmation closes an item.

### Scenario F — Quarterly review / Çeyrek kontrolü

1. The campaign (seeded) lists every active and suspended grant. **can.ozturk** keeps plain items with Bulk Keep, then decides the flagged ones individually (e.g. elif's imported Jira, an imported baseline).
2. serkan.tas reviews Salesforce: leyla's temporary grant, and oya's suspended one individually (suspended items are excluded from Bulk Keep). A Tool Owner's own access, if any, goes to the InfoSec pool. (serkan.tas has no demo password; a Super Admin can issue him an activation link.)
3. Day 7: reminders. Day 14: **selin.aydin** may decide what is left. Day 21: the rest become findings.
4. A **Super Admin** exports the quarterly evidence report from Kanıt raporları.

### Scenario G — Audit question / Denetim sorusu

**gizem.arslan** answers "Who had Jira access on the last day of last quarter, and who approved it?" with Geçmiş erişim sorgusu (state, evidence and approvers), then cross-checks the events in Olay geçmişi and exports them as CSV. She cannot change anything.

### Scenario H — The only Super Admin leaves / Tek Süper Yönetici ayrılıyor

1. HR tries to terminate admin → `409 last_super_admin`.
2. HR nominates a successor → InfoSec authorizes → the nominee accepts with the 24-hour link.
3. Now admin is no longer the last Super Admin; HR terminates them normally.

---

## 16. Implemented, simulated and future / Uygulanan, simüle edilen ve gelecek

> **Kısaca (TR):** Neyin gerçekten çalıştığını, neyin yerel bir taklitle çalıştığını ve neyin yapılmadığını ayırır.

### Implemented / Uygulandı

- Requests (new, level change, extension, on behalf), multi-step approvals with separation of duties, reassignment, override, direct grants
- API, Manual and Vendor fulfillment; verification; retries; manual retry; task queues
- Expiry, voluntary and manager removal
- Joiners (activation links), transfers with retention, suspension and reactivation, immediate and scheduled termination, rehire
- Super Admin bootstrap, two-person promotion, the last-Super-Admin guard, HR succession, `recover-admin`
- Quarterly reviews with day-7/14/21 steps and findings
- Imports (tool read, CSV, onboarding workbook), orphan findings
- Exceptions; Audit Explorer (events, CSV export, point-in-time access, approval evidence, reports); the read-only Auditor
- Append-only audit log; login rate limiting; password policy; Swagger UI (demo mode)

### Simulated / Simüle

- **Tool integrations:** the Mock Tool Service stands in for every API tool (AWS Console, Jira, Salesforce).
- **E-mail:** the Mock Email Outbox stands in for delivery; a Super Admin reveals activation links. Not a production e-mail security model.
- **Demo data and passwords:** local demo only.

### Done by people / İnsan işi

- Making Manual changes in GitHub, Figma and similar; dealing with vendors
- Exporting tool user lists for CSV import; keeping HR data current
- Hosting, HTTPS, secrets, backups, monitoring

### Future / Gelecek (not built)

- Real tool adapters behind `access.ToolClient`; real e-mail
- SSO and MFA
- A formal review of AccessFlow's own privileged roles (today a report, D11)
- Running more than one `app` instance and multiple timezones (one of each is assumed)

See [docs/known-limitations.md](known-limitations.md) and [docs/rollout-30-days.md](rollout-30-days.md).

---

## 17. Practical FAQ / Sık sorulan sorular

**Q: Can I approve my own request? / Kendi talebimi onaylayabilir miyim?**
No. Nobody approves, fulfils or reviews their own access. A step that would fall to you is routed to someone else.

**Q: My manager is on leave; my request is stuck. / Yöneticim izinde, talebim bekliyor.**
There is no delegation. InfoSec or a Super Admin reassigns the step to another eligible approver, with a reason. The waiting time is visible in the inbox.

**Q: The task says "confirmed" but I still can't log in to GitHub. / Görev tamamlandı görünüyor ama GitHub'a giremiyorum.**
Manual results are self-reported. Contact the admin who confirmed it (shown on the grant); AccessFlow cannot see inside GitHub.

**Q: Why is my access still "Kaldırma talep edildi" after I gave it up? / Erişimi bıraktım, neden hâlâ kaldırma bekliyor?**
For Manual and Vendor tools a person must remove it in the tool and confirm. Until then it counts as held, on purpose.

**Q: Does a missed review remove access? / Kontrol cevapsız kalırsa erişim kalkar mı?**
No. Day 21 creates a finding for InfoSec; access is never removed automatically by a missed deadline.

**Q: Will I get e-mail reminders? / E-posta hatırlatması gelir mi?**
No. The only mock e-mails are the manager's notice of a new request, activation links and the successor's link. Everything else is in-app.

**Q: Can a Super Admin give anyone anything? / Süper Yönetici her şeyi verebilir mi?**
They can override a step or grant directly (at most 90 days, never to themselves). Both are recorded under their own name, notified to InfoSec and the manager, and listed in a dedicated report. The control is visibility, not prevention.

**Q: An employee came back after a year. Do they get their old access? / Geri dönen çalışan eski erişimlerini alır mı?**
No. Rehire restores nothing; they request again.

**Q: What if the Mock Tool Service is down? / Sahte servis çökerse?**
Operations are retried 5 times with backoff, then become `FAILED` in Exceptions; nothing is treated as success. After it is back, retry.

**Q: Is the access shown in AccessFlow the truth? / AccessFlow'daki erişim gerçeği mi gösterir?**
For API tools it is verified against the tool. For Manual and Vendor tools it is what admins confirmed, and is labelled that way. Imported access is labelled imported.

**Q: Who sees the audit log? / Denetim kaydını kim görür?**
Super Admins and InfoSec company-wide, managers for their current reports, and the Auditor read-only.

**Q: Why can't the Auditor see the evidence reports? / Denetçi neden kanıt raporlarını göremiyor?**
An approved decision (O7, 2026-10-31): the Auditor reads raw events, point-in-time access and approval evidence; reports stay with Super Admins and InfoSec, who can export them for the Auditor.

**Q: Can HR terminate the only Super Admin? / İK tek Süper Yöneticiyi işten çıkarabilir mi?**
Not until a successor accepts. HR may suspend them at once in an emergency.

---

## 18. Short interview answers (English)

**What is AccessFlow in one sentence?**
An access-governance app: people request tool access, the right people approve it, it is granted and verified by the tool's own method, removed when it should be, and every step is evidence.

**What was the hardest design decision?**
Treating Manual and Vendor tools honestly. AccessFlow cannot see inside them, so their results are self-reported by a named admin and labelled that way everywhere, instead of pretending they are verified.

**How do you make sure leavers lose access?**
Termination removes every grant through the normal workflow at the highest priority, and each removal stays in Exceptions until it is verified or confirmed. A rehire restores nothing.

**How do approvals adapt to sensitivity?**
Each access level defines its steps: the manager always, plus the Tool Owner and/or InfoSec. Each step is a different person, and nobody approves their own access.

**Why no automatic removal in reviews?**
Removing access because someone missed a deadline breaks real work. Unanswered items become visible findings for InfoSec instead; owners are chased on day 7 and escalated on day 14.

**Why PostgreSQL as the queue instead of a message broker?**
The approval, grant, operation and audit event commit in one transaction, so there is no inconsistency window and no extra service. The load is hundreds of operations a day.

**How do you stop one admin from abusing power?**
Two-person promotion for Super Admins, no self-grants, override and direct grants recorded under the admin's name and reported, Super Admins can remove but never keep in reviews, and an independent read-only Auditor.

**What happens if the last Super Admin leaves?**
They cannot be terminated until a successor exists. HR nominates, an independent InfoSec member authorizes, and the nominee accepts in their own account. A server-side command is the last resort.

**What is simulated?**
The tool integrations (a local Mock Tool Service) and e-mail (a local outbox). The governance logic, data, audit and workers are real.

**How would you connect real AWS or GitHub?**
Implement the runner's two-method `ToolClient` interface (apply an operation, read an account) with the provider's API. Approvals, audit and lifecycle logic stay as they are.

**How is it tested?**
Business rules as table-driven pure functions; database behaviour, transactions, the audit trigger, privileges and concurrency against real PostgreSQL; API scenario tests for every CASE flow; frontend type checks and tests. All run in Docker.

**What would you do next?**
Real adapters for the most-used tools, SSO with MFA, real e-mail, and a formal review of AccessFlow's own privileged roles — as in the 30-day rollout plan.
