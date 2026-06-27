package monitor

import (
	"fmt"
	"time"

	"github.com/JongoDB-Labs/sre-v2/installer/internal/appcatalog"
	"github.com/JongoDB-Labs/sre-v2/installer/internal/tui"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// viewKind selects which dataset the table shows.
type viewKind int

const (
	viewPackages viewKind = iota
	viewApps
)

// refreshInterval is how often the background loop re-polls the cluster.
const refreshInterval = 5 * time.Second

// Run launches the k9s-style monitor: header + table + footer, with a background
// refresh loop. Read-only; views switch with 1 (packages) / 2 (apps); q quits.
func Run(version string, state appcatalog.State) error {
	tui.ApplyTheme()
	app := tview.NewApplication()

	header := tview.NewTextView().SetDynamicColors(true)
	ctx, _ := state.Kube.CurrentContext() // best-effort; header is cosmetic
	header.SetText(fmt.Sprintf("%s   context: %s", tui.Title("SRE Monitor", version), ctx))

	table := tview.NewTable().SetBorders(false).SetSelectable(true, false).SetFixed(1, 0)
	footer := tview.NewTextView().SetDynamicColors(true).
		SetText("[::b]1[::-] packages  [::b]2[::-] apps  [::b]j/k[::-] move  [::b]q[::-] quit")

	layout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(header, 1, 0, false).
		AddItem(table, 0, 1, true).
		AddItem(footer, 1, 0, false)

	m := &monitor{app: app, state: state, table: table, view: viewPackages}
	m.refresh() // initial paint

	app.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch ev.Rune() {
		case '1':
			m.view = viewPackages
			m.refresh()
			return nil
		case '2':
			m.view = viewApps
			m.refresh()
			return nil
		case 'q':
			app.Stop()
			return nil
		case 'j':
			return tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone)
		case 'k':
			return tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone)
		}
		if ev.Key() == tcell.KeyEscape {
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
	app   *tview.Application
	state appcatalog.State
	table *tview.Table
	view  viewKind
}

// refresh re-fetches the active view's data and repaints the table. Fetch errors
// are shown in-table rather than crashing the console.
func (m *monitor) refresh() {
	switch m.view {
	case viewApps:
		m.paintApps()
	default:
		m.paintPackages()
	}
}

// paintPackages fills the table from the live UDS Packages.
func (m *monitor) paintPackages() {
	raw, err := m.state.Kube.ListPackages()
	if err != nil {
		m.paintError("packages", err)
		return
	}
	rows, err := buildPackageRows(raw)
	if err != nil {
		m.paintError("packages", err)
		return
	}
	m.table.Clear()
	m.setHeaderRow("NAMESPACE", "PACKAGE", "PHASE", "ENDPOINTS")
	for i, r := range rows {
		m.table.SetCellSimple(i+1, 0, r.Namespace)
		m.table.SetCellSimple(i+1, 1, r.Name)
		m.table.GetCell(i+1, 1).SetReference(r) // for future drill-in
		m.table.SetCell(i+1, 2, phaseCell(r.Phase))
		m.table.SetCellSimple(i+1, 3, fmt.Sprintf("%d", r.Endpoints))
	}
}

// paintApps fills the table from the install records joined with live presence.
func (m *monitor) paintApps() {
	recs, err := m.state.Load()
	if err != nil {
		m.paintError("apps", err)
		return
	}
	live, err := m.state.InstalledPackages()
	if err != nil {
		m.paintError("apps", err)
		return
	}
	rows := buildAppRows(recs, live)
	m.table.Clear()
	m.setHeaderRow("APP", "VERSION", "SOURCE", "LIVE")
	for i, r := range rows {
		m.table.SetCellSimple(i+1, 0, r.Name)
		m.table.SetCellSimple(i+1, 1, r.Version)
		m.table.SetCellSimple(i+1, 2, r.Source)
		m.table.SetCell(i+1, 3, liveCell(r.Live))
	}
}

// setHeaderRow writes the fixed, non-selectable header row.
func (m *monitor) setHeaderRow(cols ...string) {
	for c, name := range cols {
		cell := tview.NewTableCell(name).SetSelectable(false).SetAttributes(tcell.AttrBold)
		m.table.SetCell(0, c, cell)
	}
}

// paintError replaces the table with a single error row.
func (m *monitor) paintError(view string, err error) {
	m.table.Clear()
	m.setHeaderRow(view)
	m.table.SetCell(1, 0, tview.NewTableCell(fmt.Sprintf("error: %v", err)).
		SetTextColor(tcell.ColorRed))
}

// phaseCell colors a package phase (Ready green, anything else amber).
func phaseCell(phase string) *tview.TableCell {
	c := tview.NewTableCell(phase)
	if phase == "Ready" {
		return c.SetTextColor(tcell.ColorGreen)
	}
	return c.SetTextColor(tcell.ColorYellow)
}

// liveCell renders the apps-view live flag.
func liveCell(live bool) *tview.TableCell {
	if live {
		return tview.NewTableCell("yes").SetTextColor(tcell.ColorGreen)
	}
	return tview.NewTableCell("DRIFT").SetTextColor(tcell.ColorRed)
}
