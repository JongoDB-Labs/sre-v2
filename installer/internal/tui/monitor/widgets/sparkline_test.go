package widgets

import "testing"

func TestSpark_ScalesMinToMax(t *testing.T) {
	got := Spark([]float64{0, 50, 100})
	want := "▁▄█" // min→lowest, max→highest
	if got != want {
		t.Fatalf("Spark = %q, want %q", got, want)
	}
}

func TestSpark_Flat(t *testing.T) {
	if got := Spark([]float64{5, 5, 5}); got != "▁▁▁" {
		t.Fatalf("flat series should be all-low, got %q", got)
	}
}

func TestSpark_Empty(t *testing.T) {
	if Spark(nil) != "" {
		t.Fatal("empty input should produce empty string")
	}
}
