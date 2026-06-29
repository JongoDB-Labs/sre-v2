package widgets

import (
	"strings"
	"testing"
)

func TestBar_FillAndColour(t *testing.T) {
	got := Bar(80, 10) // 80% of 10 = 8 filled
	if !strings.Contains(got, strings.Repeat("█", 8)) || !strings.Contains(got, strings.Repeat("░", 2)) {
		t.Fatalf("fill wrong: %q", got)
	}
	if !strings.Contains(got, "80%") {
		t.Fatalf("missing label: %q", got)
	}
	if !strings.Contains(got, "#d29922") { // amber for 70–89
		t.Fatalf("expected amber tag for 80%%: %q", got)
	}
}

func TestBar_Thresholds(t *testing.T) {
	if !strings.Contains(Bar(50, 10), "#3fb950") { // green
		t.Error("50% should be green")
	}
	if !strings.Contains(Bar(95, 10), "#f85149") { // red
		t.Error("95% should be red")
	}
}

func TestBar_Clamps(t *testing.T) {
	if strings.Count(Bar(250, 10), "█") != 10 {
		t.Error("over-100 should clamp to full")
	}
	if strings.Count(Bar(-5, 10), "█") != 0 {
		t.Error("negative should clamp to empty")
	}
}
