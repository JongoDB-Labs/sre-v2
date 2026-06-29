package data

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// PostureCheck is one continuous-monitoring posture line for the compliance view.
type PostureCheck struct{ Name, Status, Detail string }

const (
	PosturePASS = "PASS"
	PostureWARN = "WARN"
	PostureFAIL = "FAIL"
	PostureNA   = "—"
)

// auditJobList is the subset of `kubectl get jobs -A -o json` we read.
type auditJobList struct {
	Items []struct {
		Metadata struct{ Name, Namespace string } `json:"metadata"`
		Status   struct {
			Succeeded      int    `json:"succeeded"`
			Failed         int    `json:"failed"`
			StartTime      string `json:"startTime"`
			CompletionTime string `json:"completionTime"`
		} `json:"status"`
	} `json:"items"`
}

// AuditChainCheck reports the integrity of the audit hash-chain from the latest
// audit-verification Job (discovered generically by a "verify-audit" name match
// across all namespaces — app-agnostic). PASS if it succeeded, FAIL if it failed,
// "—" if no such job exists.
func AuditChainCheck(jobsJSON []byte) PostureCheck {
	out := PostureCheck{Name: "Audit-chain integrity", Status: PostureNA, Detail: "no audit-chain verification job found"}
	var list auditJobList
	if err := json.Unmarshal(jobsJSON, &list); err != nil {
		return out
	}
	latestIdx, latest := -1, ""
	for i, it := range list.Items {
		if !strings.Contains(it.Metadata.Name, "verify-audit") {
			continue
		}
		when := it.Status.StartTime
		if when >= latest { // RFC3339 strings sort chronologically
			latest, latestIdx = when, i
		}
	}
	if latestIdx < 0 {
		return out
	}
	j := list.Items[latestIdx]
	when := j.Status.CompletionTime
	if when == "" {
		when = j.Status.StartTime
	}
	pretty := when
	if t, err := time.Parse(time.RFC3339, when); err == nil {
		pretty = t.UTC().Format("2006-01-02 15:04")
	}
	if j.Status.Failed > 0 || j.Status.Succeeded == 0 {
		return PostureCheck{Name: out.Name, Status: PostureFAIL, Detail: fmt.Sprintf("%s/%s verification FAILED (%s)", j.Metadata.Namespace, j.Metadata.Name, pretty)}
	}
	return PostureCheck{Name: out.Name, Status: PosturePASS, Detail: fmt.Sprintf("chain intact — verified %s (%s)", pretty, j.Metadata.Namespace)}
}

// AlertsCheck summarizes firing Prometheus alerts by severity (skipping the
// synthetic Watchdog). FAIL on any critical, WARN on any warning, else PASS.
func AlertsCheck(alerts []Sample) PostureCheck {
	crit, warn := 0, 0
	for _, a := range alerts {
		name := a.Labels["alertname"]
		if name == "Watchdog" {
			continue
		}
		switch a.Labels["severity"] {
		case "critical":
			crit++
		case "warning":
			warn++
		}
	}
	switch {
	case crit > 0:
		return PostureCheck{"Firing alerts", PostureFAIL, fmt.Sprintf("%d critical, %d warning", crit, warn)}
	case warn > 0:
		return PostureCheck{"Firing alerts", PostureWARN, fmt.Sprintf("%d warning", warn)}
	default:
		return PostureCheck{"Firing alerts", PosturePASS, "no firing alerts"}
	}
}

// FalcoCheck flags runtime-security activity: WARN if any Falco events are present.
func FalcoCheck(rows []FalcoRow) PostureCheck {
	if len(rows) > 0 {
		return PostureCheck{"Runtime security (Falco)", PostureWARN, fmt.Sprintf("%d recent event(s)", len(rows))}
	}
	return PostureCheck{"Runtime security (Falco)", PosturePASS, "no recent events"}
}

// AuditChainJobs returns `kubectl get jobs -A -o json` (audit-verification discovery).
func (execResources) AuditChainJobs() ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), resourcesTimeout)
	defer cancel()
	return exec.CommandContext(ctx, "kubectl", "get", "jobs", "-A", "-o", "json").Output()
}
