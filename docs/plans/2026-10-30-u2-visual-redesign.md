# U2 — Visual redesign from the olive login reference

Status: requested 2026-10-30 (user instruction with `design/login-reference.html`; updated the same day with a second login reference that supersedes the first). Frontend only: no API, authorization, workflow, seed or credential change.

## Reference analysis (`design/login-reference.html`)

| Element | Reference |
|---|---|
| Brand | `#A4B657` normal, `#8A9C40` hover, `#5F6E26` deep, `#F3F6E8` tint |
| Ink / muted / line / field | `#1C2110` / `#6B7160` / `#E3E6DA` / `#FAFBF7` |
| Danger | `#C2412D` on `#FBEDEA` |
| Font | Inter 400–700; heading 26px/700, −0.02em; labels 13px/600 |
| Inputs | 52px, radius 14px, 1.5px line border, field background; focus: brand border, white, 4px brand ring at 18% |
| Primary button (reference) | 54px, radius 14px, Inter 600 15.5px, white text and arrow, shadow `rgba(138,156,64,.32)`, lifts 1px on hover |
| Card | white, radius 24px, padding 44/40, olive-tinted deep shadow, rise animation |
| Background | gradient `#A9BB5E` (top) to `#9EB050` (bottom) with a soft radial light; no shapes (second reference, 2026-10-30) |

## Design tokens (`web/src/styles.css`, `:root`)

The existing token names are kept so every screen changes through them: `--accent*` become the brand family (`--accent` = brand deep for text, links and active states; new `--brand`, `--brand-hover`, `--brand-shadow` for filled brand surfaces). Ink, muted, line, surfaces and shadows take the reference's olive-neutral values; radii grow (8 / 12 / 18, modals 20); `--font` becomes Inter. Semantic colors stay distinct from the brand: green, amber, red (reference danger), blue, violet, each with a `-soft` and a new `-line` token, replacing the hard-coded border and text shades.

## Components and screens

- **Login** rebuilt on the reference: olive gradient background, white card, logo tile, icon inputs, show/hide password, 54px primary button with arrow and spinner, inline error box. Kept: validation, demo account list (second card, English labels), Demo Environment badge, footer, forced password change.
- **Shared CSS** (one stylesheet, used by every page): buttons, inputs, selects, checkboxes, switches, segmented controls, tabs, tables, cards, task tiles, badges, tags, callouts, toasts, modals, drawers, popovers, empty states, skeletons, timeline and approval chain.
- **Shell:** sidebar with brand logo tile and brand-tint active item, top bar, content background.
- **Inline colors** in `components.tsx` (logo, avatar palette) move to tokens or a palette that keeps white initials readable.
- Every page inherits the redesign through these; page files change only where they hard-code a color.

## Deviations (by intent)

- No "Beni hatırla" / "Şifremi unuttum": AccessFlow has no persistent sessions or self-service reset (F4, A4); fake controls would mislead.
- As in the second reference, the sign-in card has no subtitle; the demo accounts stay in a second card below it (the reference has none).
- "Parola" stays (the app's term on every page), not "Şifre".
- The 54px button size is used for the login and large buttons; regular buttons stay compact for tables and toolbars, with the same colors, font and radius family.
- On phones the top-bar "Erişim talep et" button shows only its icon (accessible name kept) so every control fits without horizontal scrolling.
- White text on `#A4B657` is 2.2:1, below WCAG AA. Primary buttons therefore use a deeper olive, `#66752A` (5.1:1 with white), darkening to `#5F6E26` (5.6:1) on hover, with white text and arrow as in the reference. `#A4B657` stays for the login background, logo, progress and other fills without text. Input focus borders use `#8A9C40` (3:1 against white). Disabled buttons are dimmed and exempt from contrast requirements (WCAG 1.4.3).

## Tests

Existing vitest suites (copy changes updated), `tsc --noEmit`, `vite build` via `npm run build`; a manual visual pass on the demo at :18080.
