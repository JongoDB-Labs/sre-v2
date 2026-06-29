package data

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
)

// cloneManifest transforms a source PostgresCluster's JSON into a manifest for a
// NEW cluster that restores from the source's pgBackRest backups (PGO dataSource).
// It strips runtime metadata + status + any in-place restore/manual specs, renames
// the cluster, and points spec.dataSource.postgresCluster at the source + its first
// repo. The source is NOT modified — this only produces the new manifest bytes.
func cloneManifest(sourceJSON []byte, newName string, options []string) ([]byte, error) {
	var src map[string]any
	if err := json.Unmarshal(sourceJSON, &src); err != nil {
		return nil, fmt.Errorf("parse source cluster: %w", err)
	}
	spec, _ := src["spec"].(map[string]any)
	if spec == nil {
		return nil, fmt.Errorf("source cluster has no spec")
	}
	srcMeta, _ := src["metadata"].(map[string]any)
	srcName, _ := srcMeta["name"].(string)
	namespace, _ := srcMeta["namespace"].(string)
	if srcName == "" {
		return nil, fmt.Errorf("source cluster has no metadata.name")
	}

	// First pgBackRest repo to restore from.
	repoName := ""
	if b, ok := spec["backups"].(map[string]any); ok {
		if pgb, ok := b["pgbackrest"].(map[string]any); ok {
			if repos, ok := pgb["repos"].([]any); ok && len(repos) > 0 {
				if r0, ok := repos[0].(map[string]any); ok {
					repoName, _ = r0["name"].(string)
				}
			}
			// A clone must not carry the source's in-place restore directive or manual config.
			delete(pgb, "restore")
			delete(pgb, "manual")
		}
	}
	if repoName == "" {
		return nil, fmt.Errorf("source cluster %s has no pgBackRest repo to restore from", srcName)
	}

	// Clean metadata: keep only name (new) + namespace.
	src["metadata"] = map[string]any{"name": newName, "namespace": namespace}
	delete(src, "status")

	// Point dataSource at the source cluster's backups (replaces any existing dataSource).
	pc := map[string]any{"clusterName": srcName, "repoName": repoName}
	if len(options) > 0 {
		pc["options"] = options
	}
	spec["dataSource"] = map[string]any{"postgresCluster": pc}

	return json.Marshal(src)
}

// createFromStdin runs `kubectl create -f -` with the manifest on stdin, bounded by
// actionTimeout. Returns combined output, exit code, and error — mirroring runAction.
func createFromStdin(manifest []byte) (string, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "kubectl", "create", "-f", "-")
	cmd.Stdin = bytes.NewReader(manifest)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		code = 1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
		return string(out), code, fmt.Errorf("kubectl create -f -: %w", err)
	}
	return string(out), code, nil
}

// CloneCluster gets the source PostgresCluster and creates a NEW cluster (newName)
// that restores from the source's backups. Non-destructive: the source is untouched.
func (execResources) CloneCluster(sourceNamespace, sourceName, newName string, options []string) (string, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), pgbTimeout)
	defer cancel()
	srcJSON, err := exec.CommandContext(ctx, "kubectl", "get", "postgrescluster", sourceName,
		"-n", sourceNamespace, "-o", "json").Output()
	if err != nil {
		return "", 1, fmt.Errorf("get source cluster %s/%s: %w", sourceNamespace, sourceName, err)
	}
	manifest, err := cloneManifest(srcJSON, newName, options)
	if err != nil {
		return "", 1, err
	}
	return createFromStdin(manifest)
}
