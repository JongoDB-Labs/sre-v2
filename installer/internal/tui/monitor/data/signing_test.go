package data

import (
	"fmt"
	"reflect"
	"strings"
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

// countingCosign records how many inner VerifyImage calls are made.
type countingCosign struct{ calls int }

func (c *countingCosign) VerifyImage(image string, _ VerifyConfig) error {
	c.calls++
	return nil // always succeeds
}

func TestCachingCosign_DeduplicatesSuccesses(t *testing.T) {
	inner := &countingCosign{}
	cc := &cachingCosign{inner: inner, ok: map[string]bool{}}
	cfg := VerifyConfig{}
	img := "ghcr.io/acme/app@sha256:abc123"

	for i := 0; i < 5; i++ {
		if err := cc.VerifyImage(img, cfg); err != nil {
			t.Fatalf("call %d: unexpected error: %v", i, err)
		}
	}
	if inner.calls != 1 {
		t.Fatalf("expected exactly 1 inner call for a repeated successful image, got %d", inner.calls)
	}
}

func TestCachingCosign_FailureRetries(t *testing.T) {
	failThenSucceed := &failN{failCount: 2}
	cc := &cachingCosign{inner: failThenSucceed, ok: map[string]bool{}}
	cfg := VerifyConfig{}
	img := "ghcr.io/acme/app@sha256:def456"

	// First two calls should fail (and not cache).
	for i := 0; i < 2; i++ {
		if err := cc.VerifyImage(img, cfg); err == nil {
			t.Fatalf("call %d: expected error but got nil", i)
		}
	}
	// Third call succeeds and is cached.
	if err := cc.VerifyImage(img, cfg); err != nil {
		t.Fatalf("call 3: unexpected error: %v", err)
	}
	// Fourth call should be served from cache (inner call count stays at 3).
	if err := cc.VerifyImage(img, cfg); err != nil {
		t.Fatalf("call 4 (cached): unexpected error: %v", err)
	}
	if failThenSucceed.calls != 3 {
		t.Fatalf("expected 3 inner calls (2 fail + 1 success), got %d", failThenSucceed.calls)
	}
}

// failN fails the first n calls, then succeeds.
type failN struct {
	calls     int
	failCount int
}

func (f *failN) VerifyImage(_ string, _ VerifyConfig) error {
	f.calls++
	if f.calls <= f.failCount {
		return fmt.Errorf("transient error on call %d", f.calls)
	}
	return nil
}

func TestSigningCheck_FailDetailIncludesErr(t *testing.T) {
	mixed := []ImageResult{
		{Image: "ghcr.io/acme/app@sha256:aaa", OK: true},
		{Image: "ghcr.io/acme/migrate@sha256:bbb", OK: false, Err: "no matching signatures"},
	}
	c := SigningCheck(mixed, true)
	if c.Status != PostureFAIL {
		t.Fatalf("expected FAIL, got %q", c.Status)
	}
	if !strings.Contains(c.Detail, "no matching signatures") {
		t.Fatalf("FAIL detail should include the error string, got: %q", c.Detail)
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
