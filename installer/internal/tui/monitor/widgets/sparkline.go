package widgets

// sparkTicks are the eight ramp glyphs, low to high.
var sparkTicks = []rune("▁▂▃▄▅▆▇█")

// Spark renders vals as a sparkline scaled between the series min and max. A
// flat or empty series renders at the lowest tick (or "" when empty).
func Spark(vals []float64) string {
	if len(vals) == 0 {
		return ""
	}
	min, max := vals[0], vals[0]
	for _, v := range vals {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	span := max - min
	out := make([]rune, len(vals))
	for i, v := range vals {
		idx := 0
		if span > 0 {
			idx = int((v - min) / span * float64(len(sparkTicks)-1))
		}
		out[i] = sparkTicks[idx]
	}
	return string(out)
}
