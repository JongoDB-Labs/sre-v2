package tui

import "testing"

func TestTitle(t *testing.T) {
	got := Title("SRE Setup", "1.2.3")
	want := "SRE Setup — 1.2.3"
	if got != want {
		t.Fatalf("Title = %q, want %q", got, want)
	}
}
