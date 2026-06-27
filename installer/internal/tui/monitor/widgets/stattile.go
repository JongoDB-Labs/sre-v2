package widgets

import "fmt"

// Tile renders a big-number stat with a dim label, e.g. "56 pods".
func Tile(n int, label string) string {
	return fmt.Sprintf("[#FFFFFF::b]%d[-:-:-] [#7C8694]%s[-]", n, label)
}

// Health renders a coloured ✓/⚠/✗ rollup.
func Health(ok, warn, fail int) string {
	return fmt.Sprintf("[#3fb950]✓ %d[-]  [#d29922]⚠ %d[-]  [#f85149]✗ %d[-]", ok, warn, fail)
}
