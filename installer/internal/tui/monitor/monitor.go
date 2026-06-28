package monitor

import (
	"context"
	"fmt"
	"os/exec"
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
// and columns (set by fetchPackages/fetchApps), so no redundant fields here.
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

	main := tview.NewPages().
		AddPage("overview", overviewTV, true, true).
		AddPage("table", table, true, false)

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
	}
	m.cmdBar = cmdBar

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
		"packages":  {fetch: m.fetchPackages},
		"apps":      {fetch: m.fetchApps},
		"nodes":     {fetch: m.fetchNodes},
		"pods":      {fetch: m.fetchPods},
		"workloads": {fetch: m.fetchWorkloads},
		"services":  {fetch: m.fetchServices},
	}
	m.viewOrder = []string{"overview", "nodes", "pods", "workloads", "services", "packages", "apps"}
	m.view = "overview"
	m.setHeader("OVERVIEW", 0) // initial header before the first fetch lands

	app.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		// Pass-through guard: let the focused InputField receive all keystrokes
		// so the global rune hotkeys don't intercept characters being typed in
		// the command bar (e.g. typing "pods" would otherwise fire hotkey 'p').
		if m.app.GetFocus() == m.cmdBar {
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
		case tcell.KeyTab:
			// Cycle through m.viewOrder (overview → packages → apps → overview).
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
		app.QueueUpdate(func() {
			m.prom.Ref = ref
			m.ctx = cx
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

	if err := app.SetRoot(layout, true).Run(); err != nil {
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

// refresh kicks off a background fetch for the current view and draws the result
// via QueueUpdateDraw. It must be called on the UI goroutine (it reads m.view);
// the fetch itself runs off it, so cluster I/O never blocks input.
func (m *monitor) refresh() {
	view := m.view
	prom := m.prom // captured on the UI goroutine; the fetch reads this copy
	go func() {
		if view == "overview" {
			in := m.fetchOverview(prom)
			m.app.QueueUpdateDraw(func() {
				if m.view != view {
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
			if m.view != view {
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
		res.rows = append(res.rows, []*tview.TableCell{cell(r.Name), cell(r.Roles), statusCell(r.Status), cell(r.Version)})
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
			cell(r.Namespace), cell(r.Name), cell(r.Ready), statusCell(r.Status), cell(fmt.Sprintf("%d", r.Restarts)), cell(r.Node),
		})
	}
	return res
}

// fetchWorkloads builds the workloads table (deployments + statefulsets + daemonsets, off the UI goroutine).
func (m *monitor) fetchWorkloads() tableResult {
	res := tableResult{title: "WORKLOADS"}
	specs := []struct{ arg, kind string }{{"deployments", "Deployment"}, {"statefulsets", "StatefulSet"}, {"daemonsets", "DaemonSet"}}
	var all []data.WorkloadRow
	for _, s := range specs {
		raw, err := m.res.Get(s.arg, "-A")
		if err != nil {
			return tableResult{title: "WORKLOADS", notice: "error: " + err.Error(), isError: true}
		}
		rows, err := data.WorkloadRows(raw, s.kind)
		if err != nil {
			return tableResult{title: "WORKLOADS", notice: "error: " + err.Error(), isError: true}
		}
		all = append(all, rows...)
	}
	if len(all) == 0 {
		res.notice = "no workloads"
		return res
	}
	res.cols = []string{"NAMESPACE", "KIND", "NAME", "READY"}
	for _, r := range all {
		res.rows = append(res.rows, []*tview.TableCell{cell(r.Namespace), cell(r.Kind), cell(r.Name), cell(r.Ready)})
	}
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
		res.rows = append(res.rows, []*tview.TableCell{cell(r.Namespace), cell(r.Name), cell(r.Type), cell(r.Ports)})
	}
	return res
}

// liveCell renders the apps-view live flag.
func liveCell(live bool) *tview.TableCell {
	if live {
		return tview.NewTableCell("yes  ").SetTextColor(statusGreen)
	}
	return tview.NewTableCell("DRIFT  ").SetTextColor(statusRed)
}

// footerText is the hotkey bar (bright keys, dim labels).
func footerText() string {
	return "  [#FFFFFF::b]0[-:-:-] [#7C8694]overview[-]   " +
		"[#FFFFFF::b]1[-:-:-] [#7C8694]packages[-]   [#FFFFFF::b]2[-:-:-] [#7C8694]apps[-]   " +
		"[#FFFFFF::b]3[-:-:-] [#7C8694]nodes[-]   [#FFFFFF::b]4[-:-:-] [#7C8694]pods[-]   " +
		"[#FFFFFF::b]5[-:-:-] [#7C8694]workloads[-]   [#FFFFFF::b]6[-:-:-] [#7C8694]services[-]   " +
		"[#FFFFFF::b]Tab[-:-:-] [#7C8694]cycle[-]   [#FFFFFF::b]j/k[-:-:-] [#7C8694]move[-]   " +
		"[#FFFFFF::b]:[-:-:-] [#7C8694]jump[-]   [#FFFFFF::b]q[-:-:-] [#7C8694]quit[-]"
}
