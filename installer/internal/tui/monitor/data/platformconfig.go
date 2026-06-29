package data

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"

	"github.com/JongoDB-Labs/sre-v2/installer/internal/config"
	"gopkg.in/yaml.v3"
)

// ConfigRow is one KEY/VALUE line of the platform config view.
type ConfigRow struct{ Key, Value string }

// ConfigRows parses the srectl-config ConfigMap JSON (kubectl get cm -o json),
// pulls data["answers.yaml"], and renders the install config as rows. nil on error.
func ConfigRows(cmJSON []byte) []ConfigRow {
	var cm struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(cmJSON, &cm); err != nil {
		return nil
	}
	var a config.Answers
	if err := yaml.Unmarshal([]byte(cm.Data["answers.yaml"]), &a); err != nil {
		return nil
	}
	rows := []ConfigRow{
		{"Posture", string(a.Posture)},
		{"Sizing", string(a.Sizing)},
		{"Services", strings.Join(a.Services, ", ")},
		{"SSO", string(a.SSO)},
		{"Domain", a.Domain},
		{"Secrets", string(a.Secrets)},
	}
	if a.OIDCIssuer != "" {
		rows = append(rows, ConfigRow{"OIDC issuer", a.OIDCIssuer})
	}
	return rows
}

// PlatformConfig returns the srectl-config ConfigMap (sre-system) JSON.
func (execResources) PlatformConfig() ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), resourcesTimeout)
	defer cancel()
	return exec.CommandContext(ctx, "kubectl", "get", "configmap", "srectl-config", "-n", "sre-system", "-o", "json").Output()
}
