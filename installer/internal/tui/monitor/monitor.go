package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/JongoDB-Labs/sre-v2/installer/internal/appcatalog"
	"github.com/JongoDB-Labs/sre-v2/installer/internal/tui"
	"github.com/JongoDB-Labs/sre-v2/installer/internal/tui/monitor/data"
	"github.com/JongoDB-Labs/sre-v2/installer/internal/tui/monitor/views"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// tableView is a registered table screen. fetch is called off the UI goroutine
// and returns the rows to draw; the returned tableResult carries its own title
// and columns (set by each view's fetch method), so no redundant fields here.
type tableView struct {
	fetch func() tableResult
}

// refreshInterval is how often the background loop re-polls the cluster.
const refreshInterval = 5 * time.Second

// kubectlTimeout bounds each kubectl shell-out so a stalled API call cannot pile
// up background fetches.
const kubectlTimeout = 4 * time.Second

// Console palette: a dark canvas — this is a live monitor, not the light install
// wizard — sharing the wizard's accent / selection / status hues for cohesion.
var (
	consoleBg   = tcell.NewRGBColor(26, 30, 38)    // #1A1E26 dark slate canvas
	consoleText = tcell.NewRGBColor(214, 218, 224) // #D6DAE0 primary text
	consoleDim  = tcell.NewRGBColor(124, 134, 148) // #7C8694 labels / secondary
	statusGreen = tcell.NewRGBColor(63, 185, 80)   // #3FB950 Ready / yes
	statusAmber = tcell.NewRGBColor(210, 153, 34)  // #D29922 Pending / other
	statusRed   = tcell.NewRGBColor(248, 81, 73)   // #F85149 Failed / drift / error
)

// Run launches the k9s-style monitor: header + table + footer over a dark canvas,
// with a background refresh loop. Read-only; views switch with 0/1/2 or Tab; q quits.
//
// Cluster I/O NEVER runs on the tview UI goroutine: each refresh fetches in a
// background goroutine and marshals only the (cheap) draw through QueueUpdateDraw,
// so a slow or stalled kubectl/Prometheus call can never freeze input (q stays
// responsive).
func Run(version string, state appcatalog.State) error {
	tui.ApplyTheme()
	app := tview.NewApplication()

	header := tview.NewTextView().SetDynamicColors(true)
	header.SetTextColor(consoleText).SetBackgroundColor(consoleBg)

	table := tview.NewTable().SetBorders(false).SetSelectable(true, false).SetFixed(1, 0)
	table.SetBackgroundColor(consoleBg)
	table.SetSelectedStyle(tcell.StyleDefault.
		Background(tui.ColorSelectBg).Foreground(tui.ColorSelectText).Bold(true))

	footer := tview.NewTextView().SetDynamicColors(true)
	footer.SetTextColor(consoleDim).SetBackgroundColor(consoleBg)
	footer.SetText(footerText())

	cmdBar := tview.NewInputField().SetLabel(" : ").SetFieldWidth(0)
	cmdBar.SetLabelColor(tui.ColorSelectText).SetFieldTextColor(consoleText).
		SetFieldBackgroundColor(consoleBg).SetBackgroundColor(consoleBg)

	overviewTV := tview.NewTextView().SetDynamicColors(true).SetScrollable(true)
	overviewTV.SetTextColor(consoleText).SetBackgroundColor(consoleBg)
	overviewTV.SetText("  [#7C8694]loading…[-]")

	// detail pane: SetDynamicColors(false) is REQUIRED — raw YAML/log output
	// contains "[...]" that must NOT be parsed as tview color tags.
	detail := tview.NewTextView().SetDynamicColors(false).SetScrollable(true).SetWrap(false)
	detail.SetTextColor(consoleText).SetBackgroundColor(consoleBg)

	main := tview.NewPages().
		AddPage("overview", overviewTV, true, true).
		AddPage("table", table, true, false).
		AddPage("detail", detail, true, false)

	// Prometheus + the kube context are discovered off the UI thread at startup
	// (see the goroutine below), so app.Run() starts instantly. Ref stays empty
	// (→ degraded) and the context shows "…" until then.
	prom := data.Prom{Raw: data.NewRaw()}

	// bottom swaps between the footer hint bar and the : command bar.
	bottom := tview.NewPages().
		AddPage("footer", footer, true, true).
		AddPage("cmd", cmdBar, true, false)
	bottom.SetBackgroundColor(consoleBg)

	layout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(header, 2, 0, false).
		AddItem(main, 0, 1, true).
		AddItem(bottom, 1, 0, false)
	layout.SetBackgroundColor(consoleBg)

	m := &monitor{
		app: app, state: state, table: table, header: header,
		version: version, ctx: "…",
		main: main, overviewTV: overviewTV, prom: prom,
		res: data.NewResources(),
		auditor: data.NewMultiAuditor(
			data.NewFileAuditor(data.AuditPath()),
			data.NewConfigMapAuditor("sre-system", "srectl-platform-actions"),
		),
	}
	m.cmdBar = cmdBar
	m.detail = detail
	m.footer = footer

	cmdBar.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			name := strings.TrimSpace(cmdBar.GetText())
			cmdBar.SetText("")
			bottom.SwitchToPage("footer")
			m.app.SetFocus(m.main)
			if name == "overview" {
				m.setView("overview")
			} else if _, ok := m.tableViews[name]; ok {
				m.setView(name)
			}
			return
		}
		if key == tcell.KeyEscape {
			cmdBar.SetText("")
			bottom.SwitchToPage("footer")
			m.app.SetFocus(m.main)
		}
	})
	m.tableViews = map[string]tableView{
		"packages":   {fetch: m.fetchPackages},
		"apps":       {fetch: m.fetchApps},
		"nodes":      {fetch: m.fetchNodes},
		"pods":       {fetch: m.fetchPods},
		"workloads":  {fetch: m.fetchWorkloads},
		"services":   {fetch: m.fetchServices},
		"alerts":     {fetch: m.fetchAlerts},
		"falco":      {fetch: m.fetchFalco},
		"backups":    {fetch: m.fetchBackups},
		"compliance": {fetch: m.fetchCompliance},
	}
	m.viewOrder = []string{"overview", "nodes", "pods", "workloads", "services", "alerts", "falco", "backups", "compliance", "packages", "apps"}
	m.view = "overview"
	m.setHeader("OVERVIEW", 0) // initial header before the first fetch lands

	app.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		// Pass-through guard: let the focused InputField receive all keystrokes
		// so the global rune hotkeys don't intercept characters being typed in
		// the command bar (e.g. typing "pods" would otherwise fire hotkey 'p').
		if m.app.GetFocus() == m.cmdBar {
			return ev
		}
		// Modal guard: while the action menu or confirm overlay is visible, let the
		// modal handle all input (button navigation, Enter to confirm, Esc/Tab to
		// move between buttons). Global hotkeys must not fire underneath the overlay.
		if m.modalActive {
			return ev
		}
		// Detail-mode guard: when the detail pane is open, intercept close keys
		// first so 'q' closes the pane rather than quitting the app, and pass
		// everything else through so the focused TextView can handle scrolling.
		if m.inDetail {
			switch ev.Rune() {
			case 'q':
				m.closeDetail()
				return nil
			case 'd':
				m.setDrillMode("describe")
				return nil
			case 'y':
				m.setDrillMode("yaml")
				return nil
			case 'l':
				if m.drill.kind == "pods" {
					m.setDrillMode("logs")
				}
				return nil
			case 'j':
				return tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone)
			case 'k':
				return tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone)
			}
			if ev.Key() == tcell.KeyEscape {
				m.closeDetail()
				return nil
			}
			return ev
		}
		switch ev.Rune() {
		case '0', 'o':
			m.setView("overview")
			return nil
		case '1':
			m.setView("packages")
			return nil
		case '2':
			m.setView("apps")
			return nil
		case '3':
			m.setView("nodes")
			return nil
		case '4':
			m.setView("pods")
			return nil
		case '5':
			m.setView("workloads")
			return nil
		case '6':
			m.setView("services")
			return nil
		case '7':
			m.setView("alerts")
			return nil
		case '8':
			m.setView("falco")
			return nil
		case '9':
			m.setView("backups")
			return nil
		case 'e':
			if m.view == "compliance" {
				m.exportPosture()
			}
			return nil
		case 'a':
			// Open the action menu for the selected row (table views only; not while
			// in the detail pane — the inDetail guard above already returned for that).
			if m.view != "overview" {
				row, _ := m.table.GetSelection()
				if c := m.table.GetCell(row, 0); c != nil {
					if dt, ok := c.GetReference().(drillTarget); ok {
						m.openActions(dt)
					}
				}
			}
			return nil
		case ':':
			bottom.SwitchToPage("cmd")
			m.app.SetFocus(m.cmdBar)
			return nil
		case 'q':
			app.Stop()
			return nil
		case 'j':
			return tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone)
		case 'k':
			return tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone)
		}
		switch ev.Key() {
		case tcell.KeyEnter:
			// Drill into the selected row's resource (table views only).
			// Overview has no table rows; packages rows carry a PackageRow reference
			// and apps rows carry none, so the drillTarget type-assertion fails for both.
			if m.view != "overview" {
				row, _ := m.table.GetSelection()
				if c := m.table.GetCell(row, 0); c != nil {
					if dt, ok := c.GetReference().(drillTarget); ok {
						m.openDetail(dt)
					}
				}
			}
			return nil
		case tcell.KeyTab:
			// Advance through m.viewOrder, wrapping at the end.
			cur := 0
			for i, name := range m.viewOrder {
				if name == m.view {
					cur = i
					break
				}
			}
			m.setView(m.viewOrder[(cur+1)%len(m.viewOrder)])
			return nil
		case tcell.KeyEscape:
			app.Stop()
			return nil
		}
		return ev
	})

	// Startup: discover Prometheus + the kube context OFF the UI thread, then set
	// them and trigger the first refresh (all on the UI goroutine via QueueUpdate).
	// So a slow cluster can never block startup or input.
	go func() {
		ref := ""
		if svcs, gerr := m.prom.Raw.Get("/api/v1/namespaces/monitoring/services?limit=500"); gerr == nil {
			if r, derr := data.DiscoverPromRef(svcs); derr == nil {
				ref = r
			}
		}
		cx, cerr := state.Kube.CurrentContext()
		if cerr != nil || cx == "" {
			cx = "unknown"
		}
		actor := data.CurrentActor()
		app.QueueUpdate(func() {
			m.prom.Ref = ref
			m.ctx = cx
			m.actor = actor
			m.refresh()
		})
	}()

	// Background refresh loop: every tick, ask the UI goroutine to kick off a
	// fetch (QueueUpdate runs m.refresh on the main goroutine, which reads m.view
	// safely and then spawns the off-UI fetch).
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(refreshInterval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				app.QueueUpdate(func() { m.refresh() })
			}
		}
	}()
	defer close(stop)

	root := tview.NewPages().
		AddPage("main", layout, true, true)
	m.root = root

	if err := app.SetRoot(root, true).Run(); err != nil {
		return fmt.Errorf("monitor: run: %w", err)
	}
	return nil
}

// monitor holds the running view state.
type monitor struct {
	app        *tview.Application
	state      appcatalog.State
	table      *tview.Table
	header     *tview.TextView
	version    string
	ctx        string
	view       string               // "overview" or a key in tableViews
	tableViews map[string]tableView // registry of table screens
	viewOrder  []string             // ordered keys for Tab cycling (overview first)
	main       *tview.Pages
	overviewTV *tview.TextView
	prom       data.Prom
	res        data.Resources    // kubectl resource fetcher (bounded 4s per call)
	cmdBar     *tview.InputField // : command bar (hidden behind footer when idle)
	footer     *tview.TextView   // footer hint bar (retained so detail can swap its text)
	// detail pane (Task 2)
	detail    *tview.TextView // scrollable kubectl-describe output pane
	drill     drillTarget     // resource currently shown in detail
	drillMode string          // "describe" | "yaml" | "logs"
	inDetail  bool            // true while the detail page is front
	// action modal (Task 3)
	root        *tview.Pages // top-level pages: "main" (layout) + "modal" (overlay)
	modalActive bool         // true while the modal overlay is visible
	pending     action       // action selected in the menu, awaiting confirm (Task 4)
	// audit (Task 4)
	auditor data.Auditor // writes NDJSON audit entries to disk
	actor   string       // identity of the operator (discovered off-UI at startup)
}

// setView switches the active view immediately (page + selection, both cheap) and
// then kicks off an async refresh of its content. The page swap is instant so view
// switching never waits on cluster I/O. Unknown names (not "overview" and not in
// tableViews) are silently ignored.
func (m *monitor) setView(name string) {
	if name != "overview" {
		if _, ok := m.tableViews[name]; !ok {
			return
		}
	}
	m.view = name
	if name == "overview" {
		m.main.SwitchToPage("overview")
	} else {
		m.main.SwitchToPage("table")
		m.table.Select(1, 0)
	}
	m.refresh()
}

// logsTailLines is the maximum number of log lines fetched when drillMode=="logs".
const logsTailLines = 200

// openDetail enters the detail pane for a row's target, defaulting to describe.
func (m *monitor) openDetail(dt drillTarget) {
	m.drill = dt
	m.drillMode = "describe"
	m.inDetail = true
	m.detail.SetText("  loading…").ScrollToBeginning()
	m.main.SwitchToPage("detail")
	m.app.SetFocus(m.detail)
	m.setHeader(detailTitle(dt, "describe"), 0)
	m.footer.SetText(detailFooter(dt.kind == "pods"))
	m.drawDetail()
}

// drawDetail fetches the current drill mode OFF the UI goroutine and sets the
// detail text on it (anti-freeze: only the SetText draw is marshalled back, and
// it's dropped if the user has since left this target/mode).
func (m *monitor) drawDetail() {
	dt := m.drill
	mode := m.drillMode
	go func() {
		var text string
		var err error
		switch mode {
		case "yaml":
			text, err = m.res.Yaml(dt.kind, dt.namespace, dt.name)
		case "logs":
			text, err = m.res.Logs(dt.namespace, dt.name, logsTailLines)
		default:
			text, err = m.res.Describe(dt.kind, dt.namespace, dt.name)
		}
		if err != nil && text == "" {
			text = "error: " + err.Error()
		}
		m.app.QueueUpdateDraw(func() {
			// Staleness guard: drop the update if the user has already navigated
			// away from this target or mode (e.g. closed the pane or switched mode).
			if !m.inDetail || m.drill != dt || m.drillMode != mode {
				return
			}
			m.detail.SetText(text).ScrollToBeginning()
		})
	}()
}

// detailTitle is the header label for a drill view, e.g. "PODS/cosmos-abc · describe".
func detailTitle(dt drillTarget, mode string) string {
	return fmt.Sprintf("%s/%s · %s", strings.ToUpper(dt.kind), dt.name, mode)
}

// closeDetail returns from the detail pane to the table.
func (m *monitor) closeDetail() {
	m.inDetail = false
	m.main.SwitchToPage("table")
	m.app.SetFocus(m.main)
	m.footer.SetText(footerText())
	m.refresh() // restore the table header/count for the current view
}

// refresh kicks off a background fetch for the current view and draws the result
// via QueueUpdateDraw. It must be called on the UI goroutine (it reads m.view);
// the fetch itself runs off it, so cluster I/O never blocks input.
func (m *monitor) refresh() {
	if m.inDetail || m.modalActive {
		return // the detail pane or action modal owns the screen; don't clobber the
		// overlay title or waste a fetch while the user is reviewing an action
	}
	view := m.view
	prom := m.prom // captured on the UI goroutine; the fetch reads this copy
	go func() {
		if view == "overview" {
			in := m.fetchOverview(prom)
			m.app.QueueUpdateDraw(func() {
				if m.inDetail || m.modalActive || m.view != view {
					return
				}
				m.overviewTV.SetText(views.BuildOverview(in))
				m.setHeader("OVERVIEW", in.Packages)
			})
			return
		}
		tv, ok := m.tableViews[view]
		if !ok {
			return
		}
		res := tv.fetch()
		m.app.QueueUpdateDraw(func() {
			if m.inDetail || m.modalActive || m.view != view {
				return
			}
			m.drawTable(res)
		})
	}()
}

// fetchOverview gathers the cross-layer signals (off the UI goroutine). Any
// metrics failure degrades to MetricsOK=false rather than erroring.
func (m *monitor) fetchOverview(prom data.Prom) views.Inputs {
	in := views.Inputs{MetricsOK: true}

	// Package counts from the existing row builder.
	if raw, err := m.state.Kube.ListPackages(); err == nil {
		if rows, perr := buildPackageRows(raw); perr == nil {
			in.Packages = len(rows)
			ok := 0
			for _, r := range rows {
				if r.Phase == "Ready" {
					ok++
				}
			}
			in.LayerHealth = [3]int{ok, 0, len(rows) - ok}
		}
	}

	// Node/pod/namespace counts via kubectl (best-effort, bounded by a timeout).
	in.Nodes, in.Pods, in.Namespaces = counts()

	// Metrics from Prometheus (degrade on any failure or empty Ref).
	if prom.Ref == "" {
		in.MetricsOK = false
	} else {
		cpu, e1 := prom.Query(data.QNodeCPUPct)
		mem, e2 := prom.Query(data.QNodeMemPct)
		alerts, e3 := prom.Query(data.QFiringAlerts)
		if e1 != nil || e2 != nil || e3 != nil {
			in.MetricsOK = false
		} else {
			in.CPUPct = firstValue(cpu)
			in.MemPct = firstValue(mem)
			seen := make(map[string]bool)
			for _, a := range alerts {
				name := a.Labels["alertname"]
				if a.Labels["alertstate"] == "firing" && name != "" && !seen[name] {
					seen[name] = true
					in.AlertNames = append(in.AlertNames, name)
				}
			}
			in.FiringAlerts = len(in.AlertNames)
		}
		// Best-effort enrichments: a failure leaves the panel empty, it does NOT
		// flip MetricsOK (the core CPU/MEM/alerts above own that).
		if disk, err := prom.Query(data.QNodeDiskPct); err == nil {
			in.DiskPct = firstValue(disk)
		}
		if load, err := prom.Query(data.QNodeLoad); err == nil {
			in.Load = firstValue(load)
		}
		if phases, err := prom.Query(data.QPodPhase); err == nil {
			in.PodPhases = data.PodPhaseCounts(phases)
		}
		// CPU/MEM trend sparklines: last 30 minutes at 1-minute resolution.
		end := time.Now().Unix()
		start := end - 1800
		const step = int64(60)
		if s, err := prom.QueryRange(data.QNodeCPUSeries, start, end, step); err == nil && len(s) > 0 {
			in.CPUSeries = s[0].Values
		}
		if s, err := prom.QueryRange(data.QNodeMemSeries, start, end, step); err == nil && len(s) > 0 {
			in.MemSeries = s[0].Values
		}
	}
	return in
}

// tableResult is a fetched table view, built off the UI goroutine and ready to
// draw on it. cols == nil means show the notice (empty-state or error) instead.
type tableResult struct {
	title   string
	cols    []string
	rows    [][]*tview.TableCell
	notice  string
	isError bool
}

// drillTarget identifies the resource a table row points at, for Enter-to-drill.
// namespace == "" means cluster-scoped (node). kind is the kubectl resource name.
type drillTarget struct {
	kind, namespace, name string
}

// fetchPackages builds the packages table (off the UI goroutine).
func (m *monitor) fetchPackages() tableResult {
	raw, err := m.state.Kube.ListPackages()
	if err != nil {
		return tableResult{title: "PACKAGES", notice: "error: " + err.Error(), isError: true}
	}
	rows, err := buildPackageRows(raw)
	if err != nil {
		return tableResult{title: "PACKAGES", notice: "error: " + err.Error(), isError: true}
	}
	res := tableResult{title: "PACKAGES"}
	if len(rows) == 0 {
		res.notice = "no UDS Packages found"
		return res
	}
	res.cols = []string{"NAMESPACE", "PACKAGE", "PHASE", "ENDPOINTS"}
	for _, r := range rows {
		res.rows = append(res.rows, []*tview.TableCell{
			cell(r.Namespace), cell(r.Name).SetReference(r), phaseCell(r.Phase), cell(fmt.Sprintf("%d", r.Endpoints)),
		})
	}
	return res
}

// fetchApps builds the apps table (off the UI goroutine).
func (m *monitor) fetchApps() tableResult {
	recs, err := m.state.Load()
	if err != nil {
		return tableResult{title: "APPS", notice: "error: " + err.Error(), isError: true}
	}
	live, err := m.state.InstalledPackages()
	if err != nil {
		return tableResult{title: "APPS", notice: "error: " + err.Error(), isError: true}
	}
	rows := buildAppRows(recs, live)
	res := tableResult{title: "APPS"}
	if len(rows) == 0 {
		res.notice = "no apps installed — deploy one with: srectl app install <name>"
		return res
	}
	res.cols = []string{"APP", "VERSION", "SOURCE", "LIVE"}
	for _, r := range rows {
		res.rows = append(res.rows, []*tview.TableCell{
			cell(r.Name), cell(r.Version), cell(r.Source), liveCell(r.Live),
		})
	}
	return res
}

// drawTable renders a tableResult into the table (on the UI goroutine).
func (m *monitor) drawTable(res tableResult) {
	m.table.Clear()
	m.setHeader(res.title, len(res.rows))
	if res.cols == nil {
		colour := consoleDim
		if res.isError {
			colour = statusRed
		}
		m.table.SetCell(0, 0, tview.NewTableCell(res.notice).
			SetTextColor(colour).SetSelectable(false))
		return
	}
	m.setHeaderRow(res.cols...)
	for i, row := range res.rows {
		for j, c := range row {
			m.table.SetCell(i+1, j, c)
		}
	}
}

// firstValue returns the value of the first sample, or 0.
func firstValue(s []data.Sample) float64 {
	if len(s) == 0 {
		return 0
	}
	return s[0].Value
}

// counts returns node/pod/namespace counts via kubectl (best-effort, 0 on error,
// each call bounded by kubectlTimeout).
func counts() (nodes, pods, namespaces int) {
	count := func(args ...string) int {
		ctx, cancel := context.WithTimeout(context.Background(), kubectlTimeout)
		defer cancel()
		out, err := exec.CommandContext(ctx, "kubectl", args...).Output()
		if err != nil {
			return 0
		}
		return countNonEmpty(string(out))
	}
	nodes = count("get", "nodes", "--no-headers")
	pods = count("get", "pods", "-A", "--no-headers")
	namespaces = count("get", "ns", "--no-headers")
	return
}

// countNonEmpty counts non-empty lines in s.
func countNonEmpty(s string) int {
	n := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

// setHeader updates the two-line header: title + context, then view · count.
func (m *monitor) setHeader(view string, count int) {
	m.header.SetText(fmt.Sprintf(
		"[#9FB4D8::b]%s[-:-:-]   [#7C8694]context:[-] %s\n  [#FFFFFF::b]%s[-:-:-]  [#7C8694]· %d · refresh %ds[-]",
		tui.Title("SRE Monitor", m.version), m.ctx, view, count, int(refreshInterval.Seconds())))
}

// setHeaderRow writes the fixed, non-selectable, dimmed column-header row.
func (m *monitor) setHeaderRow(cols ...string) {
	for c, name := range cols {
		m.table.SetCell(0, c, tview.NewTableCell(name+"  ").
			SetSelectable(false).
			SetTextColor(consoleDim).
			SetAttributes(tcell.AttrBold))
	}
}

// cell builds a standard data cell: light text with trailing padding so the
// auto-sized columns breathe.
func cell(text string) *tview.TableCell {
	return tview.NewTableCell(text + "  ").SetTextColor(consoleText)
}

// phaseCell colours a package phase: Ready green, Failed red, anything else amber.
func phaseCell(phase string) *tview.TableCell {
	c := tview.NewTableCell(phase + "  ")
	switch phase {
	case "Ready":
		return c.SetTextColor(statusGreen)
	case "Failed":
		return c.SetTextColor(statusRed)
	default:
		return c.SetTextColor(statusAmber)
	}
}

// statusCell colours a status string: Ready/Running green, else amber.
func statusCell(s string) *tview.TableCell {
	c := tview.NewTableCell(s + "  ")
	if s == "Ready" || s == "Running" {
		return c.SetTextColor(statusGreen)
	}
	return c.SetTextColor(statusAmber)
}

// fetchNodes builds the nodes table (off the UI goroutine).
func (m *monitor) fetchNodes() tableResult {
	raw, err := m.res.Get("nodes")
	if err != nil {
		return tableResult{title: "NODES", notice: "error: " + err.Error(), isError: true}
	}
	rows, err := data.NodeRows(raw)
	if err != nil {
		return tableResult{title: "NODES", notice: "error: " + err.Error(), isError: true}
	}
	res := tableResult{title: "NODES"}
	if len(rows) == 0 {
		res.notice = "no nodes"
		return res
	}
	res.cols = []string{"NAME", "ROLES", "STATUS", "VERSION"}
	for _, r := range rows {
		res.rows = append(res.rows, []*tview.TableCell{
			cell(r.Name).SetReference(drillTarget{kind: "nodes", name: r.Name}),
			cell(r.Roles), statusCell(r.Status), cell(r.Version),
		})
	}
	return res
}

// fetchPods builds the pods table (off the UI goroutine).
func (m *monitor) fetchPods() tableResult {
	raw, err := m.res.Get("pods", "-A")
	if err != nil {
		return tableResult{title: "PODS", notice: "error: " + err.Error(), isError: true}
	}
	rows, err := data.PodRows(raw)
	if err != nil {
		return tableResult{title: "PODS", notice: "error: " + err.Error(), isError: true}
	}
	res := tableResult{title: "PODS"}
	if len(rows) == 0 {
		res.notice = "no pods"
		return res
	}
	res.cols = []string{"NAMESPACE", "NAME", "READY", "STATUS", "RESTARTS", "NODE"}
	for _, r := range rows {
		res.rows = append(res.rows, []*tview.TableCell{
			cell(r.Namespace).SetReference(drillTarget{kind: "pods", namespace: r.Namespace, name: r.Name}),
			cell(r.Name), cell(r.Ready), statusCell(r.Status), cell(fmt.Sprintf("%d", r.Restarts)), cell(r.Node),
		})
	}
	return res
}

// fetchWorkloads builds the workloads table (deployments + statefulsets + daemonsets, off the UI goroutine).
func (m *monitor) fetchWorkloads() tableResult {
	res := tableResult{title: "WORKLOADS"}
	specs := []struct{ arg, kind string }{{"deployments", "Deployment"}, {"statefulsets", "StatefulSet"}, {"daemonsets", "DaemonSet"}}
	for _, s := range specs {
		raw, err := m.res.Get(s.arg, "-A")
		if err != nil {
			return tableResult{title: "WORKLOADS", notice: "error: " + err.Error(), isError: true}
		}
		rows, err := data.WorkloadRows(raw, s.kind)
		if err != nil {
			return tableResult{title: "WORKLOADS", notice: "error: " + err.Error(), isError: true}
		}
		for _, r := range rows {
			res.rows = append(res.rows, []*tview.TableCell{
				cell(r.Namespace).SetReference(drillTarget{kind: s.arg, namespace: r.Namespace, name: r.Name}),
				cell(r.Kind), cell(r.Name), cell(r.Ready),
			})
		}
	}
	if len(res.rows) == 0 {
		res.notice = "no workloads"
		return res
	}
	res.cols = []string{"NAMESPACE", "KIND", "NAME", "READY"}
	return res
}

// fetchServices builds the services table (off the UI goroutine).
func (m *monitor) fetchServices() tableResult {
	raw, err := m.res.Get("services", "-A")
	if err != nil {
		return tableResult{title: "SERVICES", notice: "error: " + err.Error(), isError: true}
	}
	rows, err := data.ServiceRows(raw)
	if err != nil {
		return tableResult{title: "SERVICES", notice: "error: " + err.Error(), isError: true}
	}
	res := tableResult{title: "SERVICES"}
	if len(rows) == 0 {
		res.notice = "no services"
		return res
	}
	res.cols = []string{"NAMESPACE", "NAME", "TYPE", "PORTS"}
	for _, r := range rows {
		res.rows = append(res.rows, []*tview.TableCell{
			cell(r.Namespace).SetReference(drillTarget{kind: "services", namespace: r.Namespace, name: r.Name}),
			cell(r.Name), cell(r.Type), cell(r.Ports),
		})
	}
	return res
}

// falco discovery constants (the UDS Core Falco daemonset).
const (
	falcoNS        = "falco"
	falcoSelector  = "app.kubernetes.io/name=falco"
	falcoContainer = "falco"
)

// sevCell colours a severity/priority: critical→red, warning→amber, else dim.
func sevCell(s string) *tview.TableCell {
	c := tview.NewTableCell(s + "  ")
	switch strings.ToLower(s) {
	case "critical", "emergency", "alert":
		return c.SetTextColor(statusRed)
	case "warning":
		return c.SetTextColor(statusAmber)
	default:
		return c.SetTextColor(consoleDim)
	}
}

// firingAlertSamples queries Prometheus for the firing ALERTS vector.
// Returns the samples and any query error so callers can distinguish a genuine
// error from a successful-but-empty result.
func (m *monitor) firingAlertSamples() ([]data.Sample, error) {
	return m.prom.Query(data.QFiringAlerts)
}

// falcoRows fetches and parses Falco JSON-lines logs.
// Returns the parsed rows and any fetch/parse error so callers can distinguish
// a genuine error from a successful-but-empty log stream.
func (m *monitor) falcoRows() ([]data.FalcoRow, error) {
	raw, err := m.res.LogsByLabel(falcoNS, falcoSelector, falcoContainer, 200)
	if err != nil {
		return nil, err
	}
	return data.FalcoRows(raw), nil
}

func (m *monitor) fetchAlerts() tableResult {
	if m.prom.Ref == "" {
		return tableResult{title: "ALERTS", notice: "metrics unavailable (Prometheus unreachable)"}
	}
	samples, err := m.firingAlertSamples()
	if err != nil {
		return tableResult{title: "ALERTS", notice: "error: Prometheus query failed", isError: true}
	}
	rows := data.AlertRows(samples)
	res := tableResult{title: "ALERTS"}
	if len(rows) == 0 {
		res.notice = "no alerts firing"
		return res
	}
	res.cols = []string{"ALERT", "SEVERITY", "NAMESPACE"}
	for _, r := range rows {
		res.rows = append(res.rows, []*tview.TableCell{cell(r.Name), sevCell(r.Severity), cell(r.Namespace)})
	}
	return res
}

func (m *monitor) fetchFalco() tableResult {
	rows, err := m.falcoRows()
	if err != nil {
		return tableResult{title: "FALCO", notice: "error: kubectl logs failed", isError: true}
	}
	res := tableResult{title: "FALCO"}
	if len(rows) == 0 {
		res.notice = "no recent Falco events"
		return res
	}
	res.cols = []string{"TIME", "PRIORITY", "RULE", "NAMESPACE", "POD"}
	for _, r := range rows {
		res.rows = append(res.rows, []*tview.TableCell{cell(r.Time), sevCell(r.Priority), cell(r.Rule), cell(r.Namespace), cell(r.Pod)})
	}
	return res
}

// gatherPostureChecks collects the ConMon posture checks (best-effort, off the UI
// goroutine). Shared by the compliance view and the export.
func (m *monitor) gatherPostureChecks() []data.PostureCheck {
	var checks []data.PostureCheck

	// Audit-chain integrity (best-effort).
	if jobs, err := m.res.AuditChainJobs(); err == nil {
		checks = append(checks, data.AuditChainCheck(jobs))
	} else {
		checks = append(checks, data.PostureCheck{Name: "Audit-chain integrity", Status: data.PostureWARN, Detail: "unavailable: " + err.Error()})
	}

	// Firing alerts (best-effort). An unreachable Prometheus must NOT read as PASS on
	// a compliance screen — surface it as WARN "unavailable" (like the audit-chain row),
	// since "couldn't verify" is not "clean".
	if samples, err := m.firingAlertSamples(); err == nil {
		checks = append(checks, data.AlertsCheck(samples))
	} else {
		checks = append(checks, data.PostureCheck{Name: "Firing alerts", Status: data.PostureWARN, Detail: "unavailable: " + err.Error()})
	}

	// Runtime security / Falco (best-effort) — same rule: an unreachable Falco is WARN, not PASS.
	if rows, err := m.falcoRows(); err == nil {
		checks = append(checks, data.FalcoCheck(rows))
	} else {
		checks = append(checks, data.PostureCheck{Name: "Runtime security (Falco)", Status: data.PostureWARN, Detail: "unavailable: " + err.Error()})
	}

	return checks
}

// fetchCompliance builds the ConMon posture rollup (off the UI goroutine).
// All signals are best-effort: a failed source yields a WARN row rather than
// an error return. No drillTarget references — this view is read-only.
func (m *monitor) fetchCompliance() tableResult {
	checks := m.gatherPostureChecks()
	res := tableResult{title: "COMPLIANCE", cols: []string{"CHECK", "STATUS", "DETAIL"}}
	for _, c := range checks {
		res.rows = append(res.rows, []*tview.TableCell{
			cell(c.Name), postureCell(c.Status), cell(c.Detail),
		})
	}
	return res
}

// exportPosture gathers the live posture and writes a JSON ConMon artifact to the
// host, then shows the path. Non-destructive; the gather + write run off the UI
// goroutine, only the result modal is marshalled back.
func (m *monitor) exportPosture() {
	go func() {
		checks := m.gatherPostureChecks()
		stamp := time.Now().UTC().Format("20060102-150405")
		path := data.ConmonExportPath(stamp)
		raw, err := data.BuildPostureReport(checks, m.ctx, m.version, time.Now().UTC().Format(time.RFC3339))
		if err == nil {
			err = data.WriteReport(path, raw)
		}
		m.app.QueueUpdateDraw(func() {
			if err != nil {
				m.showResult("ConMon export — error", err.Error())
			} else {
				m.showResult("ConMon export", "wrote "+path)
			}
		})
	}()
}

// postureCell colors a posture status (PASS green / WARN amber / FAIL red / — dim).
func postureCell(status string) *tview.TableCell {
	c := cell(status)
	switch status {
	case data.PosturePASS:
		c.SetTextColor(statusGreen)
	case data.PostureWARN:
		c.SetTextColor(statusAmber)
	case data.PostureFAIL:
		c.SetTextColor(statusRed)
	default:
		c.SetTextColor(consoleDim)
	}
	return c
}

// fetchBackups builds the backups table from pgBackRest info per PostgresCluster
// (off the UI goroutine). Errors for individual clusters degrade gracefully (continue)
// so one unreachable repo-host pod cannot blank the entire view.
func (m *monitor) fetchBackups() tableResult {
	raw, err := m.res.PostgresClusters()
	if err != nil {
		return tableResult{title: "BACKUPS", notice: "error: " + err.Error(), isError: true}
	}
	var list struct {
		Items []struct {
			Metadata struct{ Name, Namespace string } `json:"metadata"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &list); err != nil || len(list.Items) == 0 {
		return tableResult{title: "BACKUPS", notice: "no PostgresCluster found"}
	}
	res := tableResult{title: "BACKUPS"}
	for _, it := range list.Items {
		ns, cluster := it.Metadata.Namespace, it.Metadata.Name
		pod, perr := m.res.RepoHostPod(ns, cluster)
		if perr != nil {
			continue
		}
		info, ierr := m.res.PgBackrestInfo(ns, pod)
		if ierr != nil {
			continue
		}
		for _, b := range data.BackupRows(info, cluster) {
			res.rows = append(res.rows, []*tview.TableCell{
				cell(b.Cluster).SetReference(drillTarget{kind: "postgrescluster", namespace: ns, name: cluster}),
				cell(b.Label), cell(b.Type), cell(b.Started), cell(b.Size),
			})
		}
	}
	if len(res.rows) == 0 {
		res.notice = "no backups (or pgBackRest unreachable)"
		return res
	}
	res.cols = []string{"CLUSTER", "BACKUP", "TYPE", "STARTED", "SIZE"}
	return res
}

// liveCell renders the apps-view live flag.
func liveCell(live bool) *tview.TableCell {
	if live {
		return tview.NewTableCell("yes  ").SetTextColor(statusGreen)
	}
	return tview.NewTableCell("DRIFT  ").SetTextColor(statusRed)
}

// setDrillMode switches the detail pane to a new mode and re-fetches (off-UI).
func (m *monitor) setDrillMode(mode string) {
	if m.drillMode == mode {
		return
	}
	m.drillMode = mode
	m.detail.SetText("  loading…").ScrollToBeginning()
	m.setHeader(detailTitle(m.drill, mode), 0)
	m.drawDetail()
}

// action is one Day-2 action applicable to a selected resource.
type action struct {
	label, preview, auditAction    string
	kind, namespace, name, command string                      // for the audit record
	exec                           func() (string, int, error) // runs OFF the UI goroutine (Task 4)
	needsReplicas                  bool                        // true for Scale: route to showScaleInput, not showConfirm
	needsTypedName                 bool                        // true for Delete: route to showTypedConfirm, not showConfirm
	needsRestoreName               bool                        // true for Restore: route to showRestoreInput, not showConfirm
}

// actionsFor returns the reversible Day-2 actions available for a resource.
// Returns nil when the resource kind has no supported actions (packages, apps, services).
func (m *monitor) actionsFor(dt drillTarget) []action {
	switch dt.kind {
	case "pods":
		return []action{
			{label: "Restart", auditAction: "restart-pod",
				kind: dt.kind, namespace: dt.namespace, name: dt.name,
				command: fmt.Sprintf("kubectl delete pod -n %s %s", dt.namespace, dt.name),
				preview: fmt.Sprintf("Restart pod %s/%s?\n\nDeletes the pod; its controller recreates it.", dt.namespace, dt.name),
				exec:    func() (string, int, error) { return m.res.DeletePod(dt.namespace, dt.name) }},
			{label: "Delete", auditAction: "delete", needsTypedName: true,
				kind: dt.kind, namespace: dt.namespace, name: dt.name},
		}
	case "deployments", "statefulsets":
		return []action{
			{label: "Rollout restart", auditAction: "rollout-restart",
				kind: dt.kind, namespace: dt.namespace, name: dt.name,
				command: fmt.Sprintf("kubectl rollout restart %s -n %s %s", dt.kind, dt.namespace, dt.name),
				preview: fmt.Sprintf("Rollout-restart %s %s/%s?\n\nCycles its pods with a rolling update.", dt.kind, dt.namespace, dt.name),
				exec:    func() (string, int, error) { return m.res.RolloutRestart(dt.kind, dt.namespace, dt.name) }},
			{label: "Scale", auditAction: "scale", needsReplicas: true,
				kind: dt.kind, namespace: dt.namespace, name: dt.name},
			{label: "Delete", auditAction: "delete", needsTypedName: true,
				kind: dt.kind, namespace: dt.namespace, name: dt.name},
		}
	case "daemonsets":
		return []action{
			{
				label: "Rollout restart", auditAction: "rollout-restart",
				kind: dt.kind, namespace: dt.namespace, name: dt.name,
				command: fmt.Sprintf("kubectl rollout restart %s -n %s %s", dt.kind, dt.namespace, dt.name),
				preview: fmt.Sprintf("Rollout-restart %s %s/%s?\n\nCycles its pods with a rolling update.", dt.kind, dt.namespace, dt.name),
				exec:    func() (string, int, error) { return m.res.RolloutRestart(dt.kind, dt.namespace, dt.name) },
			},
			{label: "Delete", auditAction: "delete", needsTypedName: true,
				kind: dt.kind, namespace: dt.namespace, name: dt.name},
		}
	case "nodes":
		return []action{
			{label: "Cordon", auditAction: "cordon", kind: dt.kind, name: dt.name,
				command: fmt.Sprintf("kubectl cordon %s", dt.name),
				preview: fmt.Sprintf("Cordon node %s?\n\nMarks it unschedulable (running pods keep running).", dt.name),
				exec:    func() (string, int, error) { return m.res.SetCordon(dt.name, true) }},
			{label: "Uncordon", auditAction: "uncordon", kind: dt.kind, name: dt.name,
				command: fmt.Sprintf("kubectl uncordon %s", dt.name),
				preview: fmt.Sprintf("Uncordon node %s?\n\nMarks it schedulable again.", dt.name),
				exec:    func() (string, int, error) { return m.res.SetCordon(dt.name, false) }},
		}
	case "postgrescluster":
		stamp := time.Now().UTC().Format(time.RFC3339)
		return []action{
			{
				label: "Trigger backup", auditAction: "trigger-backup",
				kind: dt.kind, namespace: dt.namespace, name: dt.name,
				command: fmt.Sprintf("kubectl patch postgrescluster %s -n %s (manual pgBackRest backup)", dt.name, dt.namespace),
				preview: fmt.Sprintf("Trigger an on-demand pgBackRest backup of %s/%s?\n\nPGO starts a backup job; nothing is destroyed.", dt.namespace, dt.name),
				exec:    func() (string, int, error) { return m.res.TriggerBackup(dt.namespace, dt.name, stamp) },
			},
			{
				label: "Restore to new cluster", auditAction: "restore-clone", needsRestoreName: true,
				kind: dt.kind, namespace: dt.namespace, name: dt.name,
				preview: fmt.Sprintf("Clone %s/%s into a NEW cluster from its latest backup (the original is untouched).", dt.namespace, dt.name),
			},
		}
	}
	return nil
}

// showModal swaps the overlay to a FRESH tview.Modal. A fresh modal always
// focuses its first button, avoiding tview's focus-index carry-over when one
// modal is reconfigured in place (which made Enter hit the wrong button).
func (m *monitor) showModal(text string, buttons []string, done func(buttonIndex int, buttonLabel string)) {
	mod := tview.NewModal().SetText(text)
	mod.AddButtons(buttons)
	mod.SetDoneFunc(done)
	m.root.RemovePage("modal")
	m.root.AddPage("modal", mod, true, true) // visible=true → shown over "main"
	m.modalActive = true
	m.app.SetFocus(mod)
}

// openActions shows the action menu modal for the selected row's resource.
// Does nothing if the resource kind has no supported actions.
func (m *monitor) openActions(dt drillTarget) {
	acts := m.actionsFor(dt)
	if len(acts) == 0 {
		return
	}
	labels := make([]string, 0, len(acts)+1)
	for _, a := range acts {
		labels = append(labels, a.label)
	}
	labels = append(labels, "Cancel")
	m.showModal(fmt.Sprintf("Actions · %s/%s", dt.kind, dt.name), labels, func(i int, label string) {
		if label == "Cancel" || i < 0 || i >= len(acts) {
			m.closeModal()
			return
		}
		if acts[i].needsTypedName {
			m.showTypedConfirm(acts[i])
			return
		}
		if acts[i].needsReplicas {
			m.showScaleInput(acts[i])
			return
		}
		if acts[i].needsRestoreName {
			m.showRestoreInput(acts[i])
			return
		}
		m.showConfirm(acts[i])
	})
}

// showConfirm replaces the modal with a fresh confirm prompt for the chosen action.
// Confirm calls executePending (off the UI goroutine); Cancel closes the modal.
func (m *monitor) showConfirm(a action) {
	m.pending = a
	m.showModal(a.preview, []string{"Confirm", "Cancel"}, func(_ int, label string) {
		if label == "Confirm" {
			m.executePending()
			return
		}
		m.closeModal()
	})
}

// closeModal removes the overlay and returns focus to the main table.
func (m *monitor) closeModal() {
	m.modalActive = false
	m.root.RemovePage("modal")
	m.app.SetFocus(m.main)
}

// executePending runs the pending action OFF the UI goroutine, records the audit
// entry (success or failure), then shows the result. Anti-freeze: the kubectl
// mutation never runs on the UI goroutine; only the result draw is marshalled back.
func (m *monitor) executePending() {
	a := m.pending
	m.showModal(a.preview+"\n\nrunning…", []string{"…"}, func(int, string) {})
	go func() {
		out, code, err := a.exec()
		entry := data.AuditEntry{
			Time:      time.Now().UTC().Format(time.RFC3339),
			Actor:     m.actor,
			Action:    a.auditAction,
			Kind:      a.kind,
			Namespace: a.namespace,
			Name:      a.name,
			Command:   a.command,
			ExitCode:  code,
			OK:        err == nil,
		}
		_ = m.auditor.Record(entry)
		title, body := "✓ "+a.auditAction, strings.TrimSpace(out)
		if err != nil {
			title = "✗ " + a.auditAction + " failed"
			if body == "" {
				body = err.Error()
			}
		}
		m.app.QueueUpdateDraw(func() { m.showResult(title, body) })
	}()
}

// showScaleInput prompts for a replica count, then runs the scale via the shared
// executePending path (off-UI + audited). A FRESH form each call (P3.1 focus lesson).
func (m *monitor) showScaleInput(a action) {
	form := tview.NewForm()
	form.SetBackgroundColor(consoleBg)
	form.AddInputField("Replicas", "", 6, func(textToCheck string, lastChar rune) bool {
		return lastChar >= '0' && lastChar <= '9' // digits only
	}, nil)
	form.AddButton("Scale", func() {
		text := form.GetFormItem(0).(*tview.InputField).GetText()
		n, err := strconv.Atoi(text)
		if err != nil || n < 0 {
			return // invalid/empty → no-op; operator can correct or Cancel
		}
		scaled := a
		scaled.command = fmt.Sprintf("kubectl scale %s -n %s %s --replicas=%d", a.kind, a.namespace, a.name, n)
		scaled.preview = fmt.Sprintf("Scale %s/%s to %d?", a.namespace, a.name, n)
		scaled.exec = func() (string, int, error) { return m.res.Scale(a.kind, a.namespace, a.name, n) }
		m.pending = scaled
		m.executePending() // running… → off-UI scale → audit → result
	})
	form.AddButton("Cancel", func() { m.closeModal() })
	form.SetBorder(true).SetTitle(fmt.Sprintf(" Scale %s/%s ", a.kind, a.name)).SetTitleColor(consoleText)
	form.SetButtonsAlign(tview.AlignCenter)
	// Center the form over "main" with a Grid (transparent margins).
	grid := tview.NewGrid().SetColumns(0, 44, 0).SetRows(0, 9, 0).AddItem(form, 1, 1, 1, 1, 0, 0, true)
	m.root.RemovePage("modal")
	m.root.AddPage("modal", grid, true, true)
	m.modalActive = true
	m.app.SetFocus(form)
}

// showRestoreInput collects the new cluster name, then clones the source into a NEW
// cluster via the audited executePending. Non-destructive → a simple input gate (no
// typed-name). Short label + fitting width keep the field inside the dialog box.
func (m *monitor) showRestoreInput(a action) {
	form := tview.NewForm()
	form.SetBackgroundColor(consoleBg)
	form.AddInputField("New cluster name", a.name+"-restore", 28, func(textToCheck string, lastChar rune) bool {
		return (lastChar >= 'a' && lastChar <= 'z') || (lastChar >= '0' && lastChar <= '9') || lastChar == '-'
	}, nil)
	form.AddButton("Restore", func() {
		newName := strings.TrimSpace(form.GetFormItem(0).(*tview.InputField).GetText())
		if newName == "" || newName == a.name {
			return // empty or same-as-source → no-op; operator can correct or Cancel
		}
		r := a
		r.command = fmt.Sprintf("kubectl create postgrescluster %s -n %s (clone of %s)", newName, a.namespace, a.name)
		r.preview = fmt.Sprintf("Clone %s/%s → new cluster %s", a.namespace, a.name, newName)
		r.exec = func() (string, int, error) { return m.res.CloneCluster(a.namespace, a.name, newName, nil) }
		m.pending = r
		m.executePending()
	})
	form.AddButton("Cancel", func() { m.closeModal() })
	form.SetBorder(true).SetTitle(fmt.Sprintf(" Restore %s/%s → new cluster ", a.kind, a.name)).SetTitleColor(consoleText)
	form.SetButtonsAlign(tview.AlignCenter)
	grid := tview.NewGrid().SetColumns(0, 60, 0).SetRows(0, 9, 0).AddItem(form, 1, 1, 1, 1, 0, 0, true)
	m.root.RemovePage("modal")
	m.root.AddPage("modal", grid, true, true)
	m.modalActive = true
	m.app.SetFocus(form)
}

// showTypedConfirm is the typed-name gate for a destructive action (spec §3.4): the
// operator must type the resource's exact name to confirm. On match it runs the
// action via the shared executePending (off-UI + audited). Fresh form per call.
func (m *monitor) showTypedConfirm(a action) {
	// The confirm prompt lives in a wrapping TextView, NOT the input-field label:
	// a long resource name in a field label overflows the dialog border (tview does
	// not wrap/truncate form-field labels). The field itself uses an empty label and
	// fill width (0) so it always stays inside the box, whatever the name length.
	prompt := tview.NewTextView().
		SetText(fmt.Sprintf("Type \"%s\" to confirm:", a.name)).
		SetWrap(true)
	prompt.SetTextColor(consoleText)
	prompt.SetBackgroundColor(consoleBg)

	form := tview.NewForm()
	form.SetBackgroundColor(consoleBg)
	form.AddInputField("", "", 0, nil, nil) // empty label + fill width → contained in the box
	form.AddButton("Delete", func() {
		typed := strings.TrimSpace(form.GetFormItem(0).(*tview.InputField).GetText())
		if typed != a.name {
			return // name mismatch (incl. empty) → no delete; operator can correct or Cancel
		}
		del := a
		del.command = fmt.Sprintf("kubectl delete %s -n %s %s", a.kind, a.namespace, a.name)
		del.preview = fmt.Sprintf("Delete %s %s/%s", a.kind, a.namespace, a.name)
		del.exec = func() (string, int, error) { return m.res.Delete(a.kind, a.namespace, a.name) }
		m.pending = del
		m.executePending()
	})
	form.AddButton("Cancel", func() { m.closeModal() })
	form.SetButtonsAlign(tview.AlignCenter)

	// Prompt (wrapping) above the form, both inside one bordered box.
	box := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(prompt, 2, 0, false).
		AddItem(form, 0, 1, true)
	box.SetBorder(true).
		SetTitle(fmt.Sprintf(" ⚠ Delete %s/%s ", a.kind, a.name)).
		SetTitleColor(statusRed)
	box.SetBackgroundColor(consoleBg)
	// Center the dialog over "main" with a Grid (transparent margins).
	grid := tview.NewGrid().SetColumns(0, 56, 0).SetRows(0, 12, 0).AddItem(box, 1, 1, 1, 1, 0, 0, true)
	m.root.RemovePage("modal")
	m.root.AddPage("modal", grid, true, true)
	m.modalActive = true
	m.app.SetFocus(form)
}

// showResult shows the action result; OK closes the overlay and refreshes the view.
func (m *monitor) showResult(title, body string) {
	m.showModal(title+"\n\n"+body, []string{"OK"}, func(int, string) {
		m.closeModal()
		m.refresh()
	})
}

// detailFooter is the hotkey bar shown while drilled into a resource.
func detailFooter(podLogs bool) string {
	logs := ""
	if podLogs {
		logs = "[#FFFFFF::b]l[-:-:-] [#7C8694]logs[-]   "
	}
	return "  [#FFFFFF::b]d[-:-:-] [#7C8694]describe[-]   [#FFFFFF::b]y[-:-:-] [#7C8694]yaml[-]   " + logs +
		"[#FFFFFF::b]j/k[-:-:-] [#7C8694]scroll[-]   [#FFFFFF::b]q/Esc[-:-:-] [#7C8694]back[-]"
}

// footerText is the hotkey bar (bright keys, dim labels).
func footerText() string {
	return "  [#FFFFFF::b]0[-:-:-] [#7C8694]overview[-]   " +
		"[#FFFFFF::b]1[-:-:-] [#7C8694]packages[-]   [#FFFFFF::b]2[-:-:-] [#7C8694]apps[-]   " +
		"[#FFFFFF::b]3[-:-:-] [#7C8694]nodes[-]   [#FFFFFF::b]4[-:-:-] [#7C8694]pods[-]   " +
		"[#FFFFFF::b]5[-:-:-] [#7C8694]workloads[-]   [#FFFFFF::b]6[-:-:-] [#7C8694]services[-]   " +
		"[#FFFFFF::b]7[-:-:-] [#7C8694]alerts[-]   [#FFFFFF::b]8[-:-:-] [#7C8694]falco[-]   " +
		"[#FFFFFF::b]9[-:-:-] [#7C8694]backups[-]   " +
		"[#FFFFFF::b]:compliance[-:-:-][#7C8694]↵[-]   " +
		"[#FFFFFF::b]e[-:-:-] [#7C8694]export[-]   " +
		"[#FFFFFF::b]Tab[-:-:-] [#7C8694]cycle[-]   [#FFFFFF::b]j/k[-:-:-] [#7C8694]move[-]   " +
		"[#FFFFFF::b]a[-:-:-] [#7C8694]actions[-]   " +
		"[#FFFFFF::b]:[-:-:-] [#7C8694]jump[-]   [#FFFFFF::b]q[-:-:-] [#7C8694]quit[-]"
}
