package monitor

import (
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

// viewKind selects which dataset the table shows.
type viewKind int

const (
	viewOverview viewKind = iota
	viewPackages
	viewApps
)

// refreshInterval is how often the background loop re-polls the cluster.
const refreshInterval = 5 * time.Second

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

// monitorCommandContext is the command builder for monitor kubectl calls (swappable in tests).
var monitorCommandContext = exec.Command

// Run launches the k9s-style monitor: header + table + footer over a dark canvas,
// with a background refresh loop. Read-only; views switch with 0/1/2 or Tab; q quits.
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

	overviewTV := tview.NewTextView().SetDynamicColors(true).SetScrollable(true)
	overviewTV.SetTextColor(consoleText).SetBackgroundColor(consoleBg)

	main := tview.NewPages().
		AddPage("overview", overviewTV, true, true).
		AddPage("table", table, true, false)

	// Discover Prometheus best-effort (degrade-safe).
	prom := data.Prom{Raw: data.NewRaw()}
	if svcs, err := prom.Raw.Get("/api/v1/namespaces/monitoring/services?limit=500"); err == nil {
		if ref, derr := data.DiscoverPromRef(svcs); derr == nil {
			prom.Ref = ref
		}
	}

	layout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(header, 2, 0, false).
		AddItem(main, 0, 1, true).
		AddItem(footer, 1, 0, false)
	layout.SetBackgroundColor(consoleBg)

	ctx, err := state.Kube.CurrentContext() // best-effort; header is cosmetic
	if err != nil || ctx == "" {
		ctx = "unknown"
	}

	m := &monitor{
		app: app, state: state, table: table, header: header,
		version: version, ctx: ctx, view: viewOverview,
		main: main, overviewTV: overviewTV, prom: prom,
	}
	m.refresh() // initial paint

	app.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch ev.Rune() {
		case '0', 'o':
			m.setView(viewOverview)
			return nil
		case '1':
			m.setView(viewPackages)
			return nil
		case '2':
			m.setView(viewApps)
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
			// Cycle overview → packages → apps → overview.
			switch m.view {
			case viewOverview:
				m.setView(viewPackages)
			case viewPackages:
				m.setView(viewApps)
			default:
				m.setView(viewOverview)
			}
			return nil
		case tcell.KeyEscape:
			app.Stop()
			return nil
		}
		return ev
	})

	// Background refresh loop: re-poll on an interval, redraw via QueueUpdateDraw.
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(refreshInterval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				app.QueueUpdateDraw(func() { m.refresh() })
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
	view       viewKind
	main       *tview.Pages
	overviewTV *tview.TextView
	prom       data.Prom
}

// setView switches the active view, resets the selection to the first row, and repaints.
func (m *monitor) setView(v viewKind) {
	m.view = v
	m.refresh()
	if v != viewOverview {
		m.table.Select(1, 0)
	}
}

// refresh re-fetches the active view's data and repaints the content area + header.
func (m *monitor) refresh() {
	switch m.view {
	case viewOverview:
		m.main.SwitchToPage("overview")
		m.paintOverview()
	case viewApps:
		m.main.SwitchToPage("table")
		m.paintApps()
	default:
		m.main.SwitchToPage("table")
		m.paintPackages()
	}
}

// paintOverview fetches the cross-layer signals and renders the dashboard. Any
// metrics failure degrades to MetricsOK=false rather than erroring.
func (m *monitor) paintOverview() {
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

	// Node/pod/namespace counts via kubectl (best-effort).
	in.Nodes, in.Pods, in.Namespaces = m.counts()

	// Metrics from Prometheus (degrade on any failure or empty Ref).
	if m.prom.Ref == "" {
		in.MetricsOK = false
	} else {
		cpu, e1 := m.prom.Query(data.QNodeCPUPct)
		mem, e2 := m.prom.Query(data.QNodeMemPct)
		alerts, e3 := m.prom.Query(data.QFiringAlerts)
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

	// Sparklines deferred to P1.2; ship instant gauges + counts + alerts.
	m.overviewTV.SetText(views.BuildOverview(in))
	m.setHeader("OVERVIEW", in.Packages)
}

// firstValue returns the value of the first sample, or 0.
func firstValue(s []data.Sample) float64 {
	if len(s) == 0 {
		return 0
	}
	return s[0].Value
}

// counts returns node/pod/namespace counts via kubectl (best-effort, 0 on error).
func (m *monitor) counts() (nodes, pods, namespaces int) {
	count := func(args ...string) int {
		out, err := monitorCommandContext("kubectl", args...).Output()
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

// paintPackages fills the table from the live UDS Packages.
func (m *monitor) paintPackages() {
	raw, err := m.state.Kube.ListPackages()
	if err != nil {
		m.paintError("PACKAGES", err)
		return
	}
	rows, err := buildPackageRows(raw)
	if err != nil {
		m.paintError("PACKAGES", err)
		return
	}
	m.table.Clear()
	m.setHeader("PACKAGES", len(rows))
	if len(rows) == 0 {
		m.emptyRow("no UDS Packages found")
		return
	}
	m.setHeaderRow("NAMESPACE", "PACKAGE", "PHASE", "ENDPOINTS")
	for i, r := range rows {
		m.table.SetCell(i+1, 0, cell(r.Namespace))
		m.table.SetCell(i+1, 1, cell(r.Name).SetReference(r))
		m.table.SetCell(i+1, 2, phaseCell(r.Phase))
		m.table.SetCell(i+1, 3, cell(fmt.Sprintf("%d", r.Endpoints)))
	}
}

// paintApps fills the table from the install records joined with live presence.
func (m *monitor) paintApps() {
	recs, err := m.state.Load()
	if err != nil {
		m.paintError("APPS", err)
		return
	}
	live, err := m.state.InstalledPackages()
	if err != nil {
		m.paintError("APPS", err)
		return
	}
	rows := buildAppRows(recs, live)
	m.table.Clear()
	m.setHeader("APPS", len(rows))
	if len(rows) == 0 {
		m.emptyRow("no apps installed — deploy one with: srectl app install <name>")
		return
	}
	m.setHeaderRow("APP", "VERSION", "SOURCE", "LIVE")
	for i, r := range rows {
		m.table.SetCell(i+1, 0, cell(r.Name))
		m.table.SetCell(i+1, 1, cell(r.Version))
		m.table.SetCell(i+1, 2, cell(r.Source))
		m.table.SetCell(i+1, 3, liveCell(r.Live))
	}
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

// emptyRow shows a dim placeholder when a view has no rows (no column header, so
// the message does not stretch the columns).
func (m *monitor) emptyRow(msg string) {
	m.table.SetCell(0, 0, tview.NewTableCell(msg).
		SetTextColor(consoleDim).SetSelectable(false))
}

// paintError replaces the table with a single error row.
func (m *monitor) paintError(view string, err error) {
	m.table.Clear()
	m.setHeader(view, 0)
	m.setHeaderRow(view)
	m.table.SetCell(1, 0, tview.NewTableCell(fmt.Sprintf("error: %v", err)).
		SetTextColor(statusRed).SetSelectable(false))
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
		"[#FFFFFF::b]Tab[-:-:-] [#7C8694]switch[-]   [#FFFFFF::b]j/k[-:-:-] [#7C8694]move[-]   [#FFFFFF::b]q[-:-:-] [#7C8694]quit[-]"
}
