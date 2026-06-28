package data

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// resourcesTimeout bounds each `kubectl get` so a stalled API call cannot hang
// the (background) fetch indefinitely.
const resourcesTimeout = 4 * time.Second

// Resources runs `kubectl get <args...> -o json`. Tests inject a fake.
type Resources interface {
	Get(args ...string) ([]byte, error)
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
				Phase            string `json:"phase"`
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
			Ready:    fmt.Sprintf("%d/%d", ready, len(it.Status.ContainerStatuses)),
			Status:   it.Status.Phase, Restarts: restarts, Node: it.Spec.NodeName,
		})
	}
	return rows, nil
}
