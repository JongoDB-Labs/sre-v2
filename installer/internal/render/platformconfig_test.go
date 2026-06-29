package render

import (
	"strings"
	"testing"

	"github.com/JongoDB-Labs/sre-v2/installer/internal/catalog"
	"github.com/JongoDB-Labs/sre-v2/installer/internal/config"
	"gopkg.in/yaml.v3"
)

func sampleAnswers() config.Answers {
	return config.Answers{
		Posture: config.PostureDoD, Sizing: config.SizingMedium,
		Services: []string{"cosmos", "falco"}, SSO: config.SSOKeycloak,
		Domain: "uds.dev", Secrets: config.SecretsSOPSAge, AgePublicKey: "age1xyz",
	}
}

func TestRenderPlatformConfig_RoundTrips(t *testing.T) {
	out, err := renderPlatformConfig(sampleAnswers())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// It's a k8s ConfigMap in sre-system/srectl-config with the answers under data.
	var cm struct {
		Kind     string                           `yaml:"kind"`
		Metadata struct{ Name, Namespace string } `yaml:"metadata"`
		Data     map[string]string                `yaml:"data"`
	}
	if err := yaml.Unmarshal([]byte(out), &cm); err != nil {
		t.Fatalf("output not valid yaml: %v", err)
	}
	if cm.Kind != "ConfigMap" || cm.Metadata.Name != "srectl-config" || cm.Metadata.Namespace != "sre-system" {
		t.Fatalf("configmap meta wrong: %+v", cm.Metadata)
	}
	// the embedded answers.yaml unmarshals back to the original Answers
	var got config.Answers
	if err := yaml.Unmarshal([]byte(cm.Data["answers.yaml"]), &got); err != nil {
		t.Fatalf("answers.yaml not valid: %v", err)
	}
	if got.Posture != config.PostureDoD || got.SSO != config.SSOKeycloak || got.Domain != "uds.dev" || len(got.Services) != 2 {
		t.Fatalf("round-trip lost data: %+v", got)
	}
}

func TestRender_IncludesPlatformConfig(t *testing.T) {
	files, err := Render(sampleAnswers(), catalog.MustLoad())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	found := false
	for _, f := range files {
		if f.Name == PlatformConfigFile && strings.Contains(f.Content, "kind: ConfigMap") {
			found = true
		}
	}
	if !found {
		t.Fatalf("Render output must include the %s config ConfigMap", PlatformConfigFile)
	}
}
