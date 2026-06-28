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
	got, _ := NodeRows([]byte(raw))
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
	got, _ := WorkloadRows([]byte(dsJSON), "DaemonSet")
	want := []WorkloadRow{{Namespace: "kube-system", Kind: "DaemonSet", Name: "rke2-canal", Ready: "1/1"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}
