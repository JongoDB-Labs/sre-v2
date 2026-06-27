// Package widgets holds the monitor's reusable terminal visual primitives — pure
// string-builders that turn metric values into tview-markup graphics. The
// builders are unit-tested; the tview rendering that displays them is smoke.
package widgets

import (
	"fmt"
	"strings"
)

// Bar renders pct (0–100) as a width-cell bar with a threshold colour and a
// trailing percent label, e.g. "[#d29922]████████░░[-] 80%". Out-of-range
// values clamp to [0,100].
func Bar(pct float64, width int) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	filled := int(pct/100*float64(width) + 0.5)
	if filled > width {
		filled = width
	}
	colour := "#3fb950" // green
	switch {
	case pct >= 90:
		colour = "#f85149" // red
	case pct >= 70:
		colour = "#d29922" // amber
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
	return fmt.Sprintf("[%s]%s[-] %d%%", colour, bar, int(pct+0.5))
}
