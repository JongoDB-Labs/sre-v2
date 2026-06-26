# srectl TUI redesign — whiptail-style wizard + k9s-style monitor (tview)

**Status:** Design — approved 2026-06-26
**Scope:** sre-v2 installer (`srectl`)
**Replaces:** the bubbletea skeleton TUI (`installer/internal/tui`)
**Why:** the installer must look and feel like Security Onion's whiptail setup; and the
same framework yields a k9s-style Day-2 monitor — one static binary, airgap-clean.

---

## 1. Goal

Rebuild `srectl`'s TUI on **tview** so that (a) `srectl install` is a **whiptail-look
wizard** (blue full-screen, gray bordered dialogs, radiolists/checklists), and (b)
`srectl monitor` is a **k9s-style live console** of the substrate + installed apps. One
Go binary, no external dialog dependency.

## 2. Licensing / branding (binding)

- **tview** (`github.com/rivo/tview`) + **tcell** (`github.com/gdamore/tcell/v2`) are MIT —
  fine to depend on and vendor.
- The whiptail/blue-dialog **look** is the generic `newt`/ncurses aesthetic (Debian
  installer, `raspi-config`, …) — **not** Security Onion's IP. We replicate it with
  **original tview code**.
- **Do NOT copy** `so-setup`/`so-whiptail` (those are under SO's license). Reference only
  the generic flow/conventions for inspiration.
- **Branding:** the title bar reads `SRE Setup — <version>` (or "Secure Runtime
  Environment"), **never** "Security Onion". Own colors and text.

## 3. Shared theme (`internal/tui/theme.go`)

- Set the global `tview.Styles` (`tview.Theme`): `PrimitiveBackgroundColor` = the SO blue;
  `ContrastBackgroundColor`/`MoreContrastBackgroundColor` = gray (dialog bodies);
  `BorderColor`/`TitleColor`/`PrimaryTextColor` chosen to match; selection = inverse.
- A helper `centeredDialog(title string, inner tview.Primitive, w, h int) tview.Primitive`
  → a `Flex` that centers a **gray bordered box** on the blue background — the whiptail
  dialog frame, reused by every wizard screen. Title format `SRE Setup — <version>`.

## 4. Install wizard (`internal/tui/wizard`)

- `srectl install` launches the tview app → a `Pages`-driven sequence:
  **welcome → posture → sizing → core services → SSO → secrets → review → deploy**.
- Screen primitives inside the centered dialog frame: `Modal` (yes/no, messages);
  `List` for radiolists (`(*)`); a checklist (List/Form `Checkbox`es, `[*]`) for core
  services; `Form` `InputField`s for domain / OIDC issuer / age key; a **masked**
  `InputField` for secrets; a `TextView` for review (the rendered `uds-config.yaml` +
  `values.overlay.yaml`); a progress view for deploy.
- **Logic-preserving front-end swap:** the wizard only POPULATES the existing `Answers`
  model; the existing **`render`** (→ `uds-config.yaml` + `values.overlay.yaml`) and the
  (future) orchestration are unchanged. bubbletea → tview is a front-end replacement only.
- Navigation: `Pages` forward/back; `Esc`/`<Cancel>` = back; re-entrant; `--from
  answers.yaml --non-interactive` headless path stays (skips the TUI entirely).

## 5. k9s-style monitor (`internal/tui/monitor`) — the terminal-native SP8 Day-2/ConMon console

- `srectl monitor` launches a tview app: a `Flex` — **header** (context: cluster / domain
  / version) → **main `Table`** (selectable rows) → **footer** (hotkeys).
- **Views**, switched k9s-style (`:` command bar or number keys): **packages** (UDS
  Packages + status), **apps** (the `sre-appcatalog-installs` records + health), **pods**
  (by namespace), **events**.
- **Live refresh:** a background goroutine polls each view's data source on an interval —
  via the **`kubectl` exec-wrapper** from the app-catalog (`internal/appcatalog`) and the
  appcatalog `State` — then `app.QueueUpdateDraw` updates the `Table`.
- **Navigation:** `SetInputCapture` — `j`/`k` or arrows (move), `Enter` (drill in:
  describe/logs in a `TextView`), `:` (switch view), `/` (filter), `q`/`Esc` (back/quit).
- Read-only first; Day-2 actions (install/remove via the app-catalog `Deploy`/`Remove`)
  hang off it later.

## 6. File structure

```
installer/
  cmd/srectl/install.go     # wire `srectl install` → tui/wizard (replaces the bubbletea call)
  cmd/srectl/monitor.go     # NEW: `srectl monitor` → tui/monitor
  internal/tui/
    theme.go                # tview.Styles + centeredDialog() — the whiptail frame
    wizard/wizard.go        # the Pages controller + the Answers-building flow
    wizard/screens.go       # per-screen builders (modal/list/checklist/form/review/deploy)
    monitor/monitor.go      # the Flex app + input capture + the refresh loop
    monitor/views.go        # the view registry (packages/apps/pods/events) → rows
```
The old bubbletea `internal/tui` content is replaced.

## 7. Testing

tview rendering is not unit-tested; keep the **logic testable behind it**:
- **Wizard:** a flow controller, unit-tested as `given inputs → produced Answers`, plus
  the existing render tests (`Answers → rendered files`). tview screens = manual/smoke.
- **Monitor:** each view's **row-builder** (`cluster data → []row`) is unit-tested with the
  app-catalog exec-wrapper **fakes**. `Table` rendering = manual/smoke.
- **Acceptance (manual):** `srectl install` on the test VM (whiptail-look wizard,
  renders config) + `srectl monitor` against the lab cluster (the live k9s view).

## 8. MVP scope

- **Build now:** the theme; the full wizard (all screens → `Answers`, dry-run deploy as
  today); `srectl monitor` with the **packages + apps** views (read-only, live refresh,
  navigation).
- **Defer:** the pods/events views; drill-in (describe/logs); the monitor's Day-2 actions;
  deploy-progress streaming (ties to the orchestration wiring, build-order step 2).

## 9. Dependencies

Add `github.com/rivo/tview` + `github.com/gdamore/tcell/v2` (MIT); remove the bubbletea/
lipgloss deps. Single static binary preserved (no whiptail/dialog runtime dependency).
