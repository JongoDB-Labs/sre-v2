// Package monitor implements srectl's k9s-style live console of the substrate.
// The per-view row-builders are pure (cluster data → []row) and unit-tested with
// the app-catalog fakes; the tview Table rendering + refresh loop is smoke-tested.
package monitor

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/JongoDB-Labs/sre-v2/installer/internal/appcatalog"
)

// PackageRow is one row of the packages view: a live UDS Package + its status.
type PackageRow struct {
	Namespace string
	Name      string
	Phase     string
	Endpoints int
}

// AppRow is one row of the apps view: an install record joined with live presence.
type AppRow struct {
	Name    string
	Version string
	Source  string
	Live    bool
}

// buildPackageRows parses `kubectl get packages -A -o json` into rows, sorted by
// (namespace, name) for stable display.
func buildPackageRows(raw []byte) ([]PackageRow, error) {
	var list struct {
		Items []struct {
			Metadata struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"metadata"`
			Status struct {
				Phase     string   `json:"phase"`
				Endpoints []string `json:"endpoints"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("monitor: parse packages json: %w", err)
	}
	rows := make([]PackageRow, 0, len(list.Items))
	for _, it := range list.Items {
		rows = append(rows, PackageRow{
			Namespace: it.Metadata.Namespace,
			Name:      it.Metadata.Name,
			Phase:     it.Status.Phase,
			Endpoints: len(it.Status.Endpoints),
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Namespace != rows[j].Namespace {
			return rows[i].Namespace < rows[j].Namespace
		}
		return rows[i].Name < rows[j].Name
	})
	return rows, nil
}

// buildAppRows joins the install records with the live-package set, sorted by
// name. Live is false when an app is recorded but has no live UDS Package (drift).
func buildAppRows(recs map[string]appcatalog.Record, live map[string]bool) []AppRow {
	rows := make([]AppRow, 0, len(recs))
	for name, r := range recs {
		rows = append(rows, AppRow{
			Name:    name,
			Version: r.Version,
			Source:  r.Source,
			Live:    live[name],
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows
}
