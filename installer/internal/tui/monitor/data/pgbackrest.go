package data

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const pgbTimeout = 10 * time.Second

// BackupRow is one pgBackRest backup for the backups view.
type BackupRow struct {
	Cluster, Label, Type, Started, Size string
}

// pgbStanza is the subset of `pgbackrest info --output=json` we surface.
type pgbStanza struct {
	Name   string `json:"name"`
	Backup []struct {
		Label     string `json:"label"`
		Type      string `json:"type"`
		Timestamp struct {
			Start int64 `json:"start"`
		} `json:"timestamp"`
		Info struct {
			Size int64 `json:"size"`
		} `json:"info"`
	} `json:"backup"`
}

// BackupRows parses `pgbackrest info --output=json` into rows tagged with the k8s
// cluster name. Newest backups last in pgBackRest output → reverse to newest-first.
func BackupRows(infoJSON []byte, cluster string) []BackupRow {
	var stanzas []pgbStanza
	if err := json.Unmarshal(infoJSON, &stanzas); err != nil {
		return nil
	}
	var rows []BackupRow
	for _, s := range stanzas {
		for _, b := range s.Backup {
			rows = append(rows, BackupRow{
				Cluster: cluster, Label: b.Label, Type: b.Type,
				Started: time.Unix(b.Timestamp.Start, 0).UTC().Format("2006-01-02 15:04"),
				Size:    humanBytes(b.Info.Size),
			})
		}
	}
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	return rows
}

// humanBytes renders a byte count as B/KB/MB/GB (1 decimal for KB+).
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

func pgbackrestInfoArgs(namespace, pod string) []string {
	return []string{"exec", "-n", namespace, pod, "-c", "pgbackrest", "--", "pgbackrest", "info", "--output=json"}
}

func triggerBackupArgs(namespace, cluster, stamp string) []string {
	return []string{"annotate", "postgrescluster", cluster, "-n", namespace,
		"postgres-operator.crunchydata.com/pgbackrest-backup=" + stamp, "--overwrite"}
}

func repoHostSelector(cluster string) string {
	return "postgres-operator.crunchydata.com/data=pgbackrest,postgres-operator.crunchydata.com/cluster=" + cluster
}

func pgbRun(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), pgbTimeout)
	defer cancel()
	return exec.CommandContext(ctx, "kubectl", args...).Output()
}

// PostgresClusters returns `kubectl get postgrescluster -A -o json`.
func (execResources) PostgresClusters() ([]byte, error) {
	return pgbRun("get", "postgrescluster", "-A", "-o", "json")
}

// RepoHostPod returns the pgBackRest repo-host pod name for a cluster (or "" + error).
func (execResources) RepoHostPod(namespace, cluster string) (string, error) {
	out, err := pgbRun("get", "pods", "-n", namespace, "-l", repoHostSelector(cluster),
		"-o", "jsonpath={.items[0].metadata.name}")
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(string(out))
	if name == "" {
		return "", fmt.Errorf("no pgBackRest repo-host pod for %s/%s", namespace, cluster)
	}
	return name, nil
}

// PgBackrestInfo runs `pgbackrest info --output=json` in the repo-host pod.
func (execResources) PgBackrestInfo(namespace, pod string) ([]byte, error) {
	return pgbRun(pgbackrestInfoArgs(namespace, pod)...)
}

// TriggerBackup annotates the PostgresCluster to start an on-demand pgBackRest backup.
func (execResources) TriggerBackup(namespace, cluster, stamp string) (string, int, error) {
	return runAction(triggerBackupArgs(namespace, cluster, stamp))
}
