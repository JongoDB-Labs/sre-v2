package render

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/JongoDB-Labs/sre-v2/installer/internal/catalog"
	"github.com/JongoDB-Labs/sre-v2/installer/internal/config"
	"gopkg.in/yaml.v3"
)

// Output filenames the installer renders into the --out directory.
const (
	// UDSConfigFile holds the UDS bundle variables.
	UDSConfigFile = "uds-config.yaml"
	// ValuesOverlayFile holds the Helm value overlay.
	ValuesOverlayFile = "values.overlay.yaml"
)

// File is one rendered artifact: its filename and YAML contents.
type File struct {
	// Name is the basename to write (e.g. "uds-config.yaml").
	Name string
	// Content is the rendered YAML, including a leading header comment.
	Content string
}

// Render produces the output files (UDS config, values overlay, and the
// srectl-config ConfigMap) from the answers and catalog. It validates the
// answers first so callers get one clear error instead of malformed output.
func Render(a config.Answers, cat *catalog.Catalog) ([]File, error) {
	if err := a.Validate(); err != nil {
		return nil, err
	}
	udsCfg, err := renderUDSConfig(a, cat)
	if err != nil {
		return nil, err
	}
	overlay, err := renderValuesOverlay(a)
	if err != nil {
		return nil, err
	}
	platformCfg, err := renderPlatformConfig(a)
	if err != nil {
		return nil, err
	}
	return []File{
		{Name: UDSConfigFile, Content: udsCfg},
		{Name: ValuesOverlayFile, Content: overlay},
		{Name: PlatformConfigFile, Content: platformCfg},
	}, nil
}

// Write renders the files and writes them into dir, creating dir if needed.
func Write(a config.Answers, cat *catalog.Catalog, dir string) ([]string, error) {
	files, err := Render(a, cat)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("render: create out dir %s: %w", dir, err)
	}
	paths := make([]string, 0, len(files))
	for _, f := range files {
		p := filepath.Join(dir, f.Name)
		if err := os.WriteFile(p, []byte(f.Content), 0o644); err != nil {
			return nil, fmt.Errorf("render: write %s: %w", p, err)
		}
		paths = append(paths, p)
	}
	return paths, nil
}

// selectedPackages returns the catalog IDs that will be deployed: every required
// entry plus the optional entries the answers selected, in catalog order.
func selectedPackages(a config.Answers, cat *catalog.Catalog) []string {
	var ids []string
	for _, e := range cat.All() {
		if e.Required || a.HasService(e.ID) {
			ids = append(ids, e.ID)
		}
	}
	return ids
}

// marshalWithHeader marshals v to YAML and prepends a header comment block.
func marshalWithHeader(header string, v any) (string, error) {
	data, err := yaml.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("render: marshal yaml: %w", err)
	}
	return header + string(data), nil
}
