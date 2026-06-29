package render

import (
	"fmt"

	"github.com/JongoDB-Labs/sre-v2/installer/internal/config"
	"gopkg.in/yaml.v3"
)

// PlatformConfigFile holds the persisted install Answers as a k8s ConfigMap, so
// the Day-2 console can show what the platform was configured as.
const PlatformConfigFile = "srectl-config-configmap.yaml"

// renderPlatformConfig serializes the answers into a sre-system/srectl-config
// ConfigMap (data.answers.yaml). yaml.v3 block-scalars the embedded YAML.
func renderPlatformConfig(a config.Answers) (string, error) {
	answersYAML, err := yaml.Marshal(a)
	if err != nil {
		return "", fmt.Errorf("marshal answers: %w", err)
	}
	cm := map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":      "srectl-config",
			"namespace": "sre-system",
			"labels":    map[string]string{"app.kubernetes.io/managed-by": "srectl"},
		},
		"data": map[string]any{"answers.yaml": string(answersYAML)},
	}
	out, err := yaml.Marshal(cm)
	if err != nil {
		return "", fmt.Errorf("marshal configmap: %w", err)
	}
	return "# srectl platform config — the install Answers, for the Day-2 console.\n" + string(out), nil
}
