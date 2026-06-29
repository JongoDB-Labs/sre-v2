package data

import "testing"

const cmJSON = `{"data":{"answers.yaml":"posture: DoD\nsizing: Medium\nservices:\n  - cosmos\n  - falco\nsso: Keycloak\ndomain: uds.dev\nsecrets: SOPSAge\nagePublicKey: age1xyz\n"}}`

func TestConfigRows(t *testing.T) {
	rows := ConfigRows([]byte(cmJSON))
	get := func(k string) string {
		for _, r := range rows {
			if r.Key == k {
				return r.Value
			}
		}
		return "<missing>"
	}
	if get("Posture") != "DoD" {
		t.Fatalf("posture: %q", get("Posture"))
	}
	if get("SSO") != "Keycloak" {
		t.Fatalf("sso: %q", get("SSO"))
	}
	if get("Domain") != "uds.dev" {
		t.Fatalf("domain: %q", get("Domain"))
	}
	if get("Services") == "<missing>" || get("Services") == "" {
		t.Fatalf("services row missing")
	}
}

func TestConfigRows_BadJSON(t *testing.T) {
	if rows := ConfigRows([]byte("not json")); rows != nil {
		t.Fatalf("bad json → nil, got %v", rows)
	}
}
