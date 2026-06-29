package data

import (
	"reflect"
	"testing"
)

const podsForImages = `{"items":[
 {"spec":{"containers":[{"image":"ghcr.io/acme/app@sha256:aaa"},{"image":"ghcr.io/acme/app@sha256:aaa"}],"initContainers":[{"image":"ghcr.io/acme/migrate@sha256:bbb"}]}},
 {"spec":{"containers":[{"image":"docker.io/library/redis:7"}]}},
 {"spec":{"containers":[{"image":"ghcr.io/acme/app@sha256:aaa"}]}}]}`

func TestRunningImages_DistinctUnderPrefix(t *testing.T) {
	got := RunningImages([]byte(podsForImages), "ghcr.io/acme/")
	want := []string{"ghcr.io/acme/app@sha256:aaa", "ghcr.io/acme/migrate@sha256:bbb"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("distinct-under-prefix: got %v want %v", got, want)
	}
}

func TestCosignVerifyArgs_Keyless(t *testing.T) {
	c := VerifyConfig{IdentityRegexp: "https://github.com/acme/.*", Issuer: "https://token.actions.githubusercontent.com", ImagePrefix: "ghcr.io/acme/"}
	got := cosignVerifyArgs("ghcr.io/acme/app@sha256:aaa", c)
	want := []string{"verify", "ghcr.io/acme/app@sha256:aaa",
		"--certificate-identity-regexp", "https://github.com/acme/.*",
		"--certificate-oidc-issuer", "https://token.actions.githubusercontent.com"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("keyless args: %v", got)
	}
}

func TestCosignVerifyArgs_Airgap(t *testing.T) {
	c := VerifyConfig{IdentityRegexp: "id", Issuer: "iss", ImagePrefix: "ghcr.io/acme/", Bundle: "/b.json", TrustedRoot: "/r.json"}
	got := cosignVerifyArgs("img", c)
	// keyless args PLUS --bundle/--trusted-root (modern airgap; NOT the deprecated --offline)
	want := []string{"verify", "img", "--certificate-identity-regexp", "id", "--certificate-oidc-issuer", "iss",
		"--bundle", "/b.json", "--trusted-root", "/r.json"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("airgap args: %v", got)
	}
}

func TestConfigured(t *testing.T) {
	if (VerifyConfig{}).Configured() {
		t.Fatal("empty config must be unconfigured")
	}
	if !(VerifyConfig{IdentityRegexp: "i", Issuer: "s", ImagePrefix: "p"}).Configured() {
		t.Fatal("id+issuer+prefix → configured")
	}
}

func TestSigningCheck(t *testing.T) {
	if c := SigningCheck(nil, false); c.Status != PostureNA {
		t.Fatalf("unconfigured → NA, got %q", c.Status)
	}
	if c := SigningCheck(nil, true); c.Status != PostureNA {
		t.Fatalf("configured but no matching images → NA, got %q (%s)", c.Status, c.Detail)
	}
	allok := []ImageResult{{Image: "a", OK: true}, {Image: "b", OK: true}}
	if c := SigningCheck(allok, true); c.Status != PosturePASS {
		t.Fatalf("all verified → PASS, got %q", c.Status)
	}
	mixed := []ImageResult{{Image: "a", OK: true}, {Image: "b", OK: false, Err: "no matching signatures"}}
	if c := SigningCheck(mixed, true); c.Status != PostureFAIL {
		t.Fatalf("any unverified → FAIL, got %q (%s)", c.Status, c.Detail)
	}
}
