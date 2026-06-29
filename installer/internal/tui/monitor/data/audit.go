package data

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
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

// auditPatch builds a strategic/merge-patch body that ADDS one data key (no
// read-modify-write). json.Marshal escapes the JSONL value safely.
func auditPatch(key, jsonl string) string {
	b, _ := json.Marshal(map[string]any{"data": map[string]string{key: jsonl}})
	return string(b)
}

type configMapAuditor struct{ namespace, name string }

// NewConfigMapAuditor records each entry as a new key in a ConfigMap (the substrate
// audit sink, spec §3.3). It bootstraps the ns+cm on first write. Best-effort: a
// failure is returned but does not panic; the monitor ignores audit errors.
func NewConfigMapAuditor(namespace, name string) Auditor {
	return configMapAuditor{namespace: namespace, name: name}
}

func (a configMapAuditor) Record(e AuditEntry) error {
	key := "a" + strconv.FormatInt(time.Now().UnixNano(), 10) // valid cm key (letter + digits)
	patch := auditPatch(key, e.JSONL())
	if err := a.run("patch", "configmap", a.name, "-n", a.namespace, "--type", "merge", "-p", patch); err != nil {
		a.ensure() // bootstrap ns + cm, then retry once
		return a.run("patch", "configmap", a.name, "-n", a.namespace, "--type", "merge", "-p", patch)
	}
	return nil
}

func (a configMapAuditor) ensure() {
	_ = a.run("create", "namespace", a.namespace)               // ignore "already exists"
	_ = a.run("create", "configmap", a.name, "-n", a.namespace) // ignore "already exists"
}

func (a configMapAuditor) run(args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "kubectl", args...).Run()
}

// multiAuditor fans Record out to all sub-auditors (e.g. host-file + ConfigMap),
// attempting every one even if an earlier sink errors; returns the first error.
type multiAuditor struct{ auditors []Auditor }

// NewMultiAuditor records each entry to all the given auditors.
func NewMultiAuditor(auditors ...Auditor) Auditor { return multiAuditor{auditors: auditors} }

func (m multiAuditor) Record(e AuditEntry) error {
	var firstErr error
	for _, a := range m.auditors {
		if err := a.Record(e); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
