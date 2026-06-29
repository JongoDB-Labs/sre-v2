package data

import (
	"encoding/json"
	"strings"
)

// inPlaceRestorePatch is the merge-patch that triggers a DESTRUCTIVE in-place
// pgBackRest restore: it enables in-place restore for the given repo AND sets the
// restore trigger annotation. PGO then wipes + restores the cluster from the backup.
func inPlaceRestorePatch(repo, stamp string, options []string) string {
	restore := map[string]any{"enabled": true, "repoName": repo}
	if len(options) > 0 {
		restore["options"] = options
	}
	b, _ := json.Marshal(map[string]any{
		"metadata": map[string]any{"annotations": map[string]string{
			"postgres-operator.crunchydata.com/pgbackrest-restore": stamp}},
		"spec": map[string]any{"backups": map[string]any{"pgbackrest": map[string]any{
			"restore": restore}}},
	})
	return string(b)
}

// RestoreInPlace performs a DESTRUCTIVE in-place restore of the cluster from its
// first pgBackRest repo (default repo1) — overwrites the cluster's data. The
// caller MUST gate this behind a typed-cluster-name confirm and supply the stamp.
func (execResources) RestoreInPlace(namespace, cluster, stamp string, options []string) (string, int, error) {
	repo := "repo1"
	if out, err := pgbRun("get", "postgrescluster", cluster, "-n", namespace,
		"-o", "jsonpath={.spec.backups.pgbackrest.repos[0].name}"); err == nil {
		if r := strings.TrimSpace(string(out)); r != "" {
			repo = r
		}
	}
	return runAction([]string{"patch", "postgrescluster", cluster, "-n", namespace,
		"--type", "merge", "-p", inPlaceRestorePatch(repo, stamp, options)})
}
