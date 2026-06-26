package appcatalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	// SystemNamespace is the substrate system namespace srectl ensures exists.
	SystemNamespace = "sre-system"
	// InstallsConfigMap is the canonical install-record ConfigMap name (spec §6).
	InstallsConfigMap = "sre-appcatalog-installs"
)

// errMissingConfigMap signals the install-record ConfigMap does not exist yet
// (first install). Load treats it as "no records", not an error.
var errMissingConfigMap = errors.New("install-record ConfigMap not found")

// Record is one app's install metadata. The ConfigMap is convenience metadata;
// the live cluster is the source of truth (spec §6).
type Record struct {
	// Version is the installed app version.
	Version string `yaml:"version"`
	// Source is the resolved source, e.g. "oci:ghcr.io/jongodb-labs/bundles/cosmos".
	Source string `yaml:"source"`
	// Digest is the deployed package's sha256 digest.
	Digest string `yaml:"digest"`
	// InstalledAt is the RFC3339 install timestamp.
	InstalledAt string `yaml:"installedAt"`
	// InstalledBy is the actor: the kubeconfig current-context user for CLI
	// invocations (OIDC sub is deferred to the srectl serve path).
	InstalledBy string `yaml:"installedBy"`
}

// Kube is the slice of `kubectl` this package orchestrates for state. We shell
// out to kubectl — never reimplement a Kubernetes client (keeps the binary slim
// and matches the orchestrate-don't-reimplement rule).
type Kube interface {
	EnsureNamespace(ns string) error
	GetConfigMap(ns, name string) ([]byte, error)
	ApplyConfigMap(ns, name string, data map[string]string) error
	ListPackages() ([]byte, error)
}

// State reads/writes the install-record ConfigMap and lists live UDS Packages.
type State struct {
	// Kube orchestrates kubectl; tests inject a fake.
	Kube Kube
}

// Load returns the install records, or an empty map when the ConfigMap is absent.
func (s State) Load() (map[string]Record, error) {
	raw, err := s.Kube.GetConfigMap(SystemNamespace, InstallsConfigMap)
	if errors.Is(err, errMissingConfigMap) {
		return map[string]Record{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("state: read %s/%s: %w", SystemNamespace, InstallsConfigMap, err)
	}
	return unmarshalRecords(raw)
}

// Put records (or replaces) an app's install metadata, ensuring the namespace
// and merging into any existing records (read-modify-write).
func (s State) Put(name string, r Record) error {
	if err := s.Kube.EnsureNamespace(SystemNamespace); err != nil {
		return fmt.Errorf("state: ensure namespace: %w", err)
	}
	recs, err := s.Load()
	if err != nil {
		return err
	}
	recs[name] = r
	return s.apply(recs)
}

// Delete prunes an app's record (no-op if absent).
func (s State) Delete(name string) error {
	recs, err := s.Load()
	if err != nil {
		return err
	}
	delete(recs, name)
	return s.apply(recs)
}

// apply marshals the records and writes them back as the ConfigMap data.
func (s State) apply(recs map[string]Record) error {
	data, err := marshalRecords(recs)
	if err != nil {
		return err
	}
	if err := s.Kube.ApplyConfigMap(SystemNamespace, InstallsConfigMap, data); err != nil {
		return fmt.Errorf("state: apply %s: %w", InstallsConfigMap, err)
	}
	return nil
}

// InstalledPackages returns the set of live UDS Package names (cluster truth),
// parsed from `kubectl get packages -A -o json`.
func (s State) InstalledPackages() (map[string]bool, error) {
	raw, err := s.Kube.ListPackages()
	if err != nil {
		return nil, fmt.Errorf("state: list packages: %w", err)
	}
	var list struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("state: parse packages json: %w", err)
	}
	out := make(map[string]bool, len(list.Items))
	for _, it := range list.Items {
		out[it.Metadata.Name] = true
	}
	return out, nil
}

// marshalRecords renders each record to a YAML string keyed by app name (the
// ConfigMap data shape: map[appName]yaml-blob).
func marshalRecords(recs map[string]Record) (map[string]string, error) {
	data := make(map[string]string, len(recs))
	for name, r := range recs {
		b, err := yaml.Marshal(r)
		if err != nil {
			return nil, fmt.Errorf("state: marshal record %q: %w", name, err)
		}
		data[name] = string(b)
	}
	return data, nil
}

// unmarshalRecords parses ConfigMap data (map[appName]yaml-blob) back to records.
// The input is YAML-encoded map[string]string (each value is itself a YAML blob).
func unmarshalRecords(cmData []byte) (map[string]Record, error) {
	var data map[string]string
	if err := yaml.Unmarshal(cmData, &data); err != nil {
		return nil, fmt.Errorf("state: parse configmap data: %w", err)
	}
	out := make(map[string]Record, len(data))
	for name, blob := range data {
		var r Record
		if err := yaml.Unmarshal([]byte(blob), &r); err != nil {
			return nil, fmt.Errorf("state: parse record %q: %w", name, err)
		}
		out[name] = r
	}
	return out, nil
}

// execKube is the real Kube wrapper that shells out to kubectl.
type execKube struct{}

// NewKube returns the production Kube wrapper.
func NewKube() Kube { return execKube{} }

// EnsureNamespace creates the namespace if absent (idempotent server-side apply).
func (execKube) EnsureNamespace(ns string) error {
	manifest := fmt.Sprintf("apiVersion: v1\nkind: Namespace\nmetadata:\n  name: %s\n", ns)
	cmd := commandContext("kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("kubectl apply namespace %s: %w: %s", ns, err, out)
	}
	return nil
}

// GetConfigMap returns the ConfigMap's .data as YAML, or errMissingConfigMap
// when the object does not exist.
func (execKube) GetConfigMap(ns, name string) ([]byte, error) {
	out, err := commandContext("kubectl", "get", "configmap", name, "-n", ns, "-o", "jsonpath={.data}").Output()
	if err != nil {
		// kubectl exits non-zero when the object is absent; treat as missing.
		return nil, errMissingConfigMap
	}
	return out, nil
}

// ApplyConfigMap server-side-applies the ConfigMap via a temp manifest file.
// It uses the package-level writeFile helper (catalog.go) so the package has a
// single file-write helper and ApplyConfigMap's production path is exercised by
// integration tests (not the unit tests, which inject fakeKube).
func (execKube) ApplyConfigMap(ns, name string, data map[string]string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: %s\n  namespace: %s\ndata:\n", name, ns)
	for k, v := range data {
		// Each record blob is written as a YAML block scalar under its key.
		fmt.Fprintf(&b, "  %s: |\n", k)
		for _, line := range strings.Split(strings.TrimRight(v, "\n"), "\n") {
			fmt.Fprintf(&b, "    %s\n", line)
		}
	}
	tmp := filepath.Join(os.TempDir(), "sre-appcatalog-installs.yaml")
	if err := writeFile(tmp, b.String()); err != nil {
		return err
	}
	defer os.Remove(tmp)
	if out, err := commandContext("kubectl", "apply", "-f", tmp).CombinedOutput(); err != nil {
		return fmt.Errorf("kubectl apply configmap: %w: %s", err, out)
	}
	return nil
}

// ListPackages returns the JSON output of `kubectl get packages -A -o json`.
func (execKube) ListPackages() ([]byte, error) {
	out, err := commandContext("kubectl", "get", "packages", "-A", "-o", "json").Output()
	if err != nil {
		return nil, fmt.Errorf("kubectl get packages: %w", err)
	}
	return out, nil
}
