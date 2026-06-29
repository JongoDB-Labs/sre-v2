package data

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// resourcesTimeout bounds each `kubectl get` so a stalled API call cannot hang
// the (background) fetch indefinitely.
const resourcesTimeout = 4 * time.Second

// Resources runs read-only kubectl against the cluster. Tests inject a fake.
type Resources interface {
	Get(args ...string) ([]byte, error)
	Describe(kind, namespace, name string) (string, error)
	Yaml(kind, namespace, name string) (string, error)
	Logs(namespace, name string, tail int) (string, error)
	LogsByLabel(namespace, selector, container string, tail int) ([]byte, error)
	DeletePod(namespace, name string) (string, int, error)
	RolloutRestart(kind, namespace, name string) (string, int, error)
	SetCordon(node string, cordon bool) (string, int, error)
	Scale(kind, namespace, name string, replicas int) (string, int, error)
	Delete(kind, namespace, name string) (string, int, error)
}

type execResources struct{}

// NewResources returns the production Resources wrapper.
func NewResources() Resources { return execResources{} }

// Get runs `kubectl get <args...> -o json`, bounded by resourcesTimeout.
func (execResources) Get(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), resourcesTimeout)
	defer cancel()
	full := append([]string{"get"}, args...)
	full = append(full, "-o", "json")
	out, err := exec.CommandContext(ctx, "kubectl", full...).Output()
	if err != nil {
		return nil, fmt.Errorf("kubectl get %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

// NodeRow is one row of the nodes view.
type NodeRow struct {
	Name, Roles, Status, Version string
}

// NodeRows parses `kubectl get nodes -o json`.
func NodeRows(raw []byte) ([]NodeRow, error) {
	var list struct {
		Items []struct {
			Metadata struct {
				Name   string            `json:"name"`
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
			Status struct {
				Conditions []struct {
					Type   string `json:"type"`
					Status string `json:"status"`
				} `json:"conditions"`
				NodeInfo struct {
					KubeletVersion string `json:"kubeletVersion"`
				} `json:"nodeInfo"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("data: parse nodes json: %w", err)
	}
	rows := make([]NodeRow, 0, len(list.Items))
	for _, it := range list.Items {
		var roles []string
		for k := range it.Metadata.Labels {
			if r, ok := strings.CutPrefix(k, "node-role.kubernetes.io/"); ok && r != "" {
				roles = append(roles, r)
			}
		}
		sort.Strings(roles)
		status := "NotReady"
		for _, c := range it.Status.Conditions {
			if c.Type == "Ready" && c.Status == "True" {
				status = "Ready"
			}
		}
		rows = append(rows, NodeRow{
			Name: it.Metadata.Name, Roles: strings.Join(roles, ","),
			Status: status, Version: it.Status.NodeInfo.KubeletVersion,
		})
	}
	return rows, nil
}

// PodRow is one row of the pods view.
type PodRow struct {
	Namespace, Name, Ready, Status string
	Restarts                       int
	Node                           string
}

// PodRows parses `kubectl get pods -A -o json`.
func PodRows(raw []byte) ([]PodRow, error) {
	var list struct {
		Items []struct {
			Metadata struct {
				Namespace string `json:"namespace"`
				Name      string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				NodeName string `json:"nodeName"`
			} `json:"spec"`
			Status struct {
				Phase             string `json:"phase"`
				ContainerStatuses []struct {
					Ready        bool `json:"ready"`
					RestartCount int  `json:"restartCount"`
				} `json:"containerStatuses"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("data: parse pods json: %w", err)
	}
	rows := make([]PodRow, 0, len(list.Items))
	for _, it := range list.Items {
		ready, restarts := 0, 0
		for _, cs := range it.Status.ContainerStatuses {
			if cs.Ready {
				ready++
			}
			restarts += cs.RestartCount
		}
		rows = append(rows, PodRow{
			Namespace: it.Metadata.Namespace, Name: it.Metadata.Name,
			Ready:  fmt.Sprintf("%d/%d", ready, len(it.Status.ContainerStatuses)),
			Status: it.Status.Phase, Restarts: restarts, Node: it.Spec.NodeName,
		})
	}
	return rows, nil
}

// WorkloadRow is one row of the workloads view (a deploy/sts/ds).
type WorkloadRow struct {
	Namespace, Kind, Name, Ready string
}

// WorkloadRows parses a deployment/statefulset/daemonset list. DaemonSets report
// readiness under different status fields than deployments/statefulsets.
func WorkloadRows(raw []byte, kind string) ([]WorkloadRow, error) {
	var list struct {
		Items []struct {
			Metadata struct {
				Namespace string `json:"namespace"`
				Name      string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				Replicas int `json:"replicas"`
			} `json:"spec"`
			Status struct {
				ReadyReplicas          int `json:"readyReplicas"`
				NumberReady            int `json:"numberReady"`
				DesiredNumberScheduled int `json:"desiredNumberScheduled"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("data: parse %s json: %w", kind, err)
	}
	rows := make([]WorkloadRow, 0, len(list.Items))
	for _, it := range list.Items {
		ready, desired := it.Status.ReadyReplicas, it.Spec.Replicas
		if kind == "DaemonSet" {
			ready, desired = it.Status.NumberReady, it.Status.DesiredNumberScheduled
		}
		rows = append(rows, WorkloadRow{
			Namespace: it.Metadata.Namespace, Kind: kind, Name: it.Metadata.Name,
			Ready: fmt.Sprintf("%d/%d", ready, desired),
		})
	}
	return rows, nil
}

// ServiceRow is one row of the services view.
type ServiceRow struct {
	Namespace, Name, Type, Ports string
}

// ServiceRows parses `kubectl get services -A -o json`.
func ServiceRows(raw []byte) ([]ServiceRow, error) {
	var list struct {
		Items []struct {
			Metadata struct {
				Namespace string `json:"namespace"`
				Name      string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				Type  string `json:"type"`
				Ports []struct {
					Port int `json:"port"`
				} `json:"ports"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("data: parse services json: %w", err)
	}
	rows := make([]ServiceRow, 0, len(list.Items))
	for _, it := range list.Items {
		ports := make([]string, 0, len(it.Spec.Ports))
		for _, p := range it.Spec.Ports {
			ports = append(ports, fmt.Sprintf("%d", p.Port))
		}
		rows = append(rows, ServiceRow{
			Namespace: it.Metadata.Namespace, Name: it.Metadata.Name,
			Type: it.Spec.Type, Ports: strings.Join(ports, ","),
		})
	}
	return rows, nil
}

// detailTimeout bounds describe/yaml/logs shell-outs (slightly longer than the
// list timeout: describe and a tailed log can be a touch slower than a get).
const detailTimeout = 8 * time.Second

// describeArgs builds `kubectl describe <kind> [-n ns] <name>`. A cluster-scoped
// resource (node) has ns == "" and omits the namespace flag.
func describeArgs(kind, namespace, name string) []string {
	args := []string{"describe", kind}
	if namespace != "" {
		args = append(args, "-n", namespace)
	}
	return append(args, name)
}

// yamlArgs builds `kubectl get <kind> [-n ns] <name> -o yaml`.
func yamlArgs(kind, namespace, name string) []string {
	args := []string{"get", kind}
	if namespace != "" {
		args = append(args, "-n", namespace)
	}
	return append(args, name, "-o", "yaml")
}

// logsArgs builds `kubectl logs -n <ns> <name> --tail <tail>` (pods only).
func logsArgs(namespace, name string, tail int) []string {
	args := []string{"logs"}
	if namespace != "" {
		args = append(args, "-n", namespace)
	}
	return append(args, name, "--tail", fmt.Sprintf("%d", tail))
}

// runDetail runs `kubectl <args...>` bounded by detailTimeout, returning combined
// output (stderr is informative on a describe/logs failure).
func runDetail(args []string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), detailTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "kubectl", args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("kubectl %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

// Describe returns `kubectl describe` text for a resource.
func (execResources) Describe(kind, namespace, name string) (string, error) {
	return runDetail(describeArgs(kind, namespace, name))
}

// Yaml returns the resource's manifest via `kubectl get -o yaml`.
func (execResources) Yaml(kind, namespace, name string) (string, error) {
	return runDetail(yamlArgs(kind, namespace, name))
}

// Logs returns the last `tail` log lines of a pod.
func (execResources) Logs(namespace, name string, tail int) (string, error) {
	return runDetail(logsArgs(namespace, name, tail))
}

// actionTimeout bounds a mutating kubectl action (restart/cordon/rollout). Drain
// (deferred) would need longer; these are quick.
const actionTimeout = 15 * time.Second

// deletePodArgs builds `kubectl delete pod -n <ns> <name>` (pod restart — the
// controller recreates it).
func deletePodArgs(namespace, name string) []string {
	return []string{"delete", "pod", "-n", namespace, name}
}

// rolloutRestartArgs builds `kubectl rollout restart <kind> -n <ns> <name>`.
func rolloutRestartArgs(kind, namespace, name string) []string {
	return []string{"rollout", "restart", kind, "-n", namespace, name}
}

// cordonArgs builds `kubectl cordon|uncordon <node>`.
func cordonArgs(node string, cordon bool) []string {
	verb := "uncordon"
	if cordon {
		verb = "cordon"
	}
	return []string{verb, node}
}

// runAction runs a mutating `kubectl <args...>` bounded by actionTimeout, returning
// combined output, the process exit code (0 on success), and error.
func runAction(args []string) (string, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "kubectl", args...).CombinedOutput()
	code := 0
	if err != nil {
		code = 1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
		return string(out), code, fmt.Errorf("kubectl %s: %w", strings.Join(args, " "), err)
	}
	return string(out), code, nil
}

// DeletePod restarts a pod by deleting it (its controller recreates it).
func (execResources) DeletePod(namespace, name string) (string, int, error) {
	return runAction(deletePodArgs(namespace, name))
}

// RolloutRestart triggers a rolling restart of a workload.
func (execResources) RolloutRestart(kind, namespace, name string) (string, int, error) {
	return runAction(rolloutRestartArgs(kind, namespace, name))
}

// SetCordon cordons (cordon=true) or uncordons a node.
func (execResources) SetCordon(node string, cordon bool) (string, int, error) {
	return runAction(cordonArgs(node, cordon))
}

// scaleArgs builds `kubectl scale <kind> -n <ns> <name> --replicas=<n>`.
func scaleArgs(kind, namespace, name string, replicas int) []string {
	return []string{"scale", kind, "-n", namespace, name, fmt.Sprintf("--replicas=%d", replicas)}
}

// Scale sets a workload's replica count.
func (execResources) Scale(kind, namespace, name string, replicas int) (string, int, error) {
	return runAction(scaleArgs(kind, namespace, name, replicas))
}

// deleteArgs builds `kubectl delete <kind> -n <ns> <name>` (the generic delete used
// by the destructive Delete action; deletePodArgs stays the pod-restart variant).
func deleteArgs(kind, namespace, name string) []string {
	return []string{"delete", kind, "-n", namespace, name}
}

// Delete removes a namespaced resource.
func (execResources) Delete(kind, namespace, name string) (string, int, error) {
	return runAction(deleteArgs(kind, namespace, name))
}
