package data

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// falcoMax caps how many Falco events the view shows (newest first).
const falcoMax = 50

// FalcoRow is one Falco runtime-security event for the security view.
type FalcoRow struct {
	Time, Priority, Rule, Namespace, Pod string
}

// falcoEvent is the subset of Falco's JSON output we surface.
type falcoEvent struct {
	Priority string `json:"priority"`
	Rule     string `json:"rule"`
	Time     string `json:"time"`
	Fields   struct {
		Namespace string `json:"k8s.ns.name"`
		Pod       string `json:"k8s.pod.name"`
	} `json:"output_fields"`
}

// FalcoRows parses Falco JSON-lines output (skipping non-JSON lines), returns the
// most recent falcoMax events newest-first. Time is shortened to HH:MM:SS when parseable.
func FalcoRows(raw []byte) []FalcoRow {
	lines := strings.Split(string(raw), "\n")
	rows := make([]FalcoRow, 0, len(lines))
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" || ln[0] != '{' {
			continue
		}
		var e falcoEvent
		if err := json.Unmarshal([]byte(ln), &e); err != nil || e.Rule == "" {
			continue
		}
		t := e.Time
		if parsed, perr := time.Parse(time.RFC3339Nano, e.Time); perr == nil {
			t = parsed.Format("15:04:05")
		}
		rows = append(rows, FalcoRow{Time: t, Priority: e.Priority, Rule: e.Rule,
			Namespace: e.Fields.Namespace, Pod: e.Fields.Pod})
	}
	// Reverse to newest-first (Falco logs are oldest→newest).
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	if len(rows) > falcoMax {
		rows = rows[:falcoMax]
	}
	return rows
}

// logsByLabelArgs builds `kubectl logs -n <ns> -l <selector> -c <container> --tail <n>`.
func logsByLabelArgs(namespace, selector, container string, tail int) []string {
	return []string{"logs", "-n", namespace, "-l", selector, "-c", container, "--tail", fmt.Sprintf("%d", tail)}
}

// LogsByLabel returns logs from pods matching a label selector (e.g. the Falco DS).
func (execResources) LogsByLabel(namespace, selector, container string, tail int) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), detailTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "kubectl", logsByLabelArgs(namespace, selector, container, tail)...).Output()
	if err != nil {
		return nil, fmt.Errorf("kubectl logs -l %s: %w", selector, err)
	}
	return out, nil
}
