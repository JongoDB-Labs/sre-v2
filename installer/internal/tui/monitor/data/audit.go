package data

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// AuditEntry is one record of an executed Day-2 action.
type AuditEntry struct {
	Time      string `json:"time"`
	Actor     string `json:"actor"`
	Action    string `json:"action"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
	Command   string `json:"command"`
	ExitCode  int    `json:"exitCode"`
	OK        bool   `json:"ok"`
}

// JSONL renders the entry as one JSON line (trailing newline) for an append log.
func (e AuditEntry) JSONL() string {
	b, _ := json.Marshal(e) // AuditEntry has only json-safe fields; Marshal cannot fail
	return string(b) + "\n"
}

// Auditor records executed actions. Tests inject a fake; the file impl appends JSONL.
type Auditor interface {
	Record(e AuditEntry) error
}

type fileAuditor struct{ path string }

// NewFileAuditor returns an Auditor that appends each entry to path (creating the
// parent dir). The substrate ConfigMap/Events sink is a future swap behind this iface.
func NewFileAuditor(path string) Auditor { return fileAuditor{path: path} }

func (a fileAuditor) Record(e AuditEntry) error {
	if err := os.MkdirAll(filepath.Dir(a.path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(a.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(e.JSONL())
	return err
}

// AuditPath is the operator-local action log: $XDG_STATE_HOME/srectl/… or ~/.local/state/srectl/…
func AuditPath() string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "srectl-platform-actions.jsonl" // last-resort cwd-relative
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "srectl", "platform-actions.jsonl")
}

// CurrentActor returns the kubeconfig current-context user (best-effort), else $USER, else "unknown".
func CurrentActor() string {
	out, err := exec.Command("kubectl", "config", "view", "--minify", "-o", "jsonpath={.contexts[0].context.user}").Output()
	if err == nil {
		if u := strings.TrimSpace(string(out)); u != "" {
			return u
		}
	}
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "unknown"
}
