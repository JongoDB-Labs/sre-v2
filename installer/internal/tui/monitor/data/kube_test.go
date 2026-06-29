package data

import (
	"reflect"
	"testing"
)

const nodesJSON = `{"items":[
 {"metadata":{"name":"cosmos-k8s","labels":{"node-role.kubernetes.io/control-plane":"true","node-role.kubernetes.io/etcd":"true","kubernetes.io/hostname":"cosmos-k8s"}},
  "status":{"conditions":[{"type":"MemoryPressure","status":"False"},{"type":"Ready","status":"True"}],"nodeInfo":{"kubeletVersion":"v1.35.5+rke2r2"}}}]}`

func TestNodeRows(t *testing.T) {
	got, err := NodeRows([]byte(nodesJSON))
	if err != nil {
		t.Fatalf("NodeRows: %v", err)
	}
	want := []NodeRow{{Name: "cosmos-k8s", Roles: "control-plane,etcd", Status: "Ready", Version: "v1.35.5+rke2r2"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestNodeRows_NotReady(t *testing.T) {
	raw := `{"items":[{"metadata":{"name":"n2","labels":{}},"status":{"conditions":[{"type":"Ready","status":"False"}],"nodeInfo":{"kubeletVersion":"v1"}}}]}`
	got, err := NodeRows([]byte(raw))
	if err != nil {
		t.Fatalf("NodeRows: %v", err)
	}
	if len(got) != 1 || got[0].Status != "NotReady" || got[0].Roles != "" {
		t.Fatalf("got %+v", got)
	}
}

const podsJSON = `{"items":[
 {"metadata":{"namespace":"authservice","name":"authservice-74b485b56-g2xdw"},"spec":{"nodeName":"cosmos-k8s"},
  "status":{"phase":"Running","containerStatuses":[{"ready":true,"restartCount":0}]}},
 {"metadata":{"namespace":"cosmos","name":"cosmos-pg-0"},"spec":{"nodeName":"cosmos-k8s"},
  "status":{"phase":"Running","containerStatuses":[{"ready":true,"restartCount":2},{"ready":false,"restartCount":1}]}}]}`

func TestPodRows(t *testing.T) {
	got, err := PodRows([]byte(podsJSON))
	if err != nil {
		t.Fatalf("PodRows: %v", err)
	}
	want := []PodRow{
		{Namespace: "authservice", Name: "authservice-74b485b56-g2xdw", Ready: "1/1", Status: "Running", Restarts: 0, Node: "cosmos-k8s"},
		{Namespace: "cosmos", Name: "cosmos-pg-0", Ready: "1/2", Status: "Running", Restarts: 3, Node: "cosmos-k8s"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

const deployJSON = `{"items":[{"metadata":{"namespace":"authservice","name":"authservice"},"spec":{"replicas":2},"status":{"readyReplicas":1}}]}`
const dsJSON = `{"items":[{"metadata":{"namespace":"kube-system","name":"rke2-canal"},"status":{"numberReady":1,"desiredNumberScheduled":1}}]}`

func TestWorkloadRows_Deployment(t *testing.T) {
	got, err := WorkloadRows([]byte(deployJSON), "Deployment")
	if err != nil {
		t.Fatalf("WorkloadRows: %v", err)
	}
	want := []WorkloadRow{{Namespace: "authservice", Kind: "Deployment", Name: "authservice", Ready: "1/2"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestWorkloadRows_DaemonSet(t *testing.T) {
	got, err := WorkloadRows([]byte(dsJSON), "DaemonSet")
	if err != nil {
		t.Fatalf("WorkloadRows: %v", err)
	}
	want := []WorkloadRow{{Namespace: "kube-system", Kind: "DaemonSet", Name: "rke2-canal", Ready: "1/1"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

const svcJSON = `{"items":[
 {"metadata":{"namespace":"authservice","name":"authservice"},"spec":{"type":"ClusterIP","ports":[{"port":10003}]}},
 {"metadata":{"namespace":"istio","name":"gw"},"spec":{"type":"LoadBalancer","ports":[{"port":80},{"port":443}]}}]}`

func TestServiceRows(t *testing.T) {
	got, err := ServiceRows([]byte(svcJSON))
	if err != nil {
		t.Fatalf("ServiceRows: %v", err)
	}
	want := []ServiceRow{
		{Namespace: "authservice", Name: "authservice", Type: "ClusterIP", Ports: "10003"},
		{Namespace: "istio", Name: "gw", Type: "LoadBalancer", Ports: "80,443"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestDescribeArgs(t *testing.T) {
	if got := describeArgs("pods", "cosmos", "cosmos-abc"); !reflect.DeepEqual(got, []string{"describe", "pods", "-n", "cosmos", "cosmos-abc"}) {
		t.Fatalf("namespaced: got %v", got)
	}
	if got := describeArgs("nodes", "", "cosmos-k8s"); !reflect.DeepEqual(got, []string{"describe", "nodes", "cosmos-k8s"}) {
		t.Fatalf("cluster-scoped: got %v", got)
	}
}

func TestYamlArgs(t *testing.T) {
	if got := yamlArgs("services", "istio", "gw"); !reflect.DeepEqual(got, []string{"get", "services", "-n", "istio", "gw", "-o", "yaml"}) {
		t.Fatalf("namespaced: got %v", got)
	}
	if got := yamlArgs("nodes", "", "cosmos-k8s"); !reflect.DeepEqual(got, []string{"get", "nodes", "cosmos-k8s", "-o", "yaml"}) {
		t.Fatalf("cluster-scoped: got %v", got)
	}
}

func TestLogsArgs(t *testing.T) {
	if got := logsArgs("cosmos", "cosmos-abc", 200); !reflect.DeepEqual(got, []string{"logs", "-n", "cosmos", "cosmos-abc", "--tail", "200"}) {
		t.Fatalf("got %v", got)
	}
}

func TestActionArgs(t *testing.T) {
	if got := deletePodArgs("cosmos", "cosmos-pg-0"); !reflect.DeepEqual(got, []string{"delete", "pod", "-n", "cosmos", "cosmos-pg-0"}) {
		t.Fatalf("deletePodArgs: %v", got)
	}
	if got := rolloutRestartArgs("deployments", "authservice", "authservice"); !reflect.DeepEqual(got, []string{"rollout", "restart", "deployments", "-n", "authservice", "authservice"}) {
		t.Fatalf("rolloutRestartArgs: %v", got)
	}
	if got := cordonArgs("cosmos-k8s", true); !reflect.DeepEqual(got, []string{"cordon", "cosmos-k8s"}) {
		t.Fatalf("cordonArgs cordon: %v", got)
	}
	if got := cordonArgs("cosmos-k8s", false); !reflect.DeepEqual(got, []string{"uncordon", "cosmos-k8s"}) {
		t.Fatalf("cordonArgs uncordon: %v", got)
	}
}

func TestScaleArgs(t *testing.T) {
	got := scaleArgs("deployments", "cosmos", "cosmos", 3)
	want := []string{"scale", "deployments", "-n", "cosmos", "cosmos", "--replicas=3"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scaleArgs: got %v want %v", got, want)
	}
	if got := scaleArgs("statefulsets", "cosmos", "cosmos-pg", 0); got[len(got)-1] != "--replicas=0" {
		t.Fatalf("scaleArgs replicas=0: %v", got)
	}
}

func TestDeleteArgs(t *testing.T) {
	got := deleteArgs("deployments", "default", "smoke-target")
	want := []string{"delete", "deployments", "-n", "default", "smoke-target"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("deleteArgs: got %v want %v", got, want)
	}
}
