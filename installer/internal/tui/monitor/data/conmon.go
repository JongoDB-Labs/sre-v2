package data

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// PostureSummary is the rollup of a posture report's checks.
type PostureSummary struct {
	Pass    int    `json:"pass"`
	Warn    int    `json:"warn"`
	Fail    int    `json:"fail"`
	NA      int    `json:"na"`
	Overall string `json:"overall"` // FAIL if any FAIL, else WARN if any WARN, else PASS
}

// PostureReport is the exportable ConMon artifact.
type PostureReport struct {
	Schema      string         `json:"schema"`
	Tool        string         `json:"tool"`
	GeneratedAt string         `json:"generatedAt"`
	Context     string         `json:"context"`
	Summary     PostureSummary `json:"summary"`
	Checks      []PostureCheck `json:"checks"`
}

const postureSchema = "srectl.conmon.posture/v1"

// BuildPostureReport renders the posture checks into an indented JSON artifact
// with a computed summary. Pure: the caller supplies the timestamp string.
func BuildPostureReport(checks []PostureCheck, kubeContext, tool, generatedAt string) ([]byte, error) {
	sum := PostureSummary{Overall: PosturePASS}
	for _, c := range checks {
		switch c.Status {
		case PosturePASS:
			sum.Pass++
		case PostureWARN:
			sum.Warn++
		case PostureFAIL:
			sum.Fail++
		default:
			sum.NA++
		}
	}
	switch {
	case sum.Fail > 0:
		sum.Overall = PostureFAIL
	case sum.Warn > 0:
		sum.Overall = PostureWARN
	default:
		sum.Overall = PosturePASS
	}
	if checks == nil {
		checks = []PostureCheck{}
	}
	return json.MarshalIndent(PostureReport{
		Schema: postureSchema, Tool: tool, GeneratedAt: generatedAt,
		Context: kubeContext, Summary: sum, Checks: checks,
	}, "", "  ")
}

// ConmonExportPath is the artifact path for a given timestamp, under the srectl
// state dir (same base as the audit log).
func ConmonExportPath(stamp string) string {
	return filepath.Join(stateDir(), "conmon-posture-"+stamp+".json")
}

// WriteReport creates the state dir if needed and writes the artifact (0644).
func WriteReport(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
