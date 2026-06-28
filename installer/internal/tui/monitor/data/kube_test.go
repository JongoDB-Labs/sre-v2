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
