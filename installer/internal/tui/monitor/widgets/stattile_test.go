package widgets

import (
	"strings"
	"testing"
)

func TestTile(t *testing.T) {
	got := Tile(56, "pods")
	if !strings.Contains(got, "56") || !strings.Contains(got, "pods") || !strings.Contains(got, "::b]") {
		t.Fatalf("Tile = %q", got)
	}
}

func TestHealth(t *testing.T) {
	got := Health(5, 1, 0)
	for _, frag := range []string{"✓ 5", "⚠ 1", "✗ 0", "#3fb950", "#f85149"} {
		if !strings.Contains(got, frag) {
			t.Fatalf("Health missing %q: %q", frag, got)
		}
	}
}
