package config

import (
	"path/filepath"
	"testing"
)

func TestDefaultValidates(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatalf("default answers should validate: %v", err)
	}
}

func TestValidate_ExternalOIDCRequiresIssuer(t *testing.T) {
	a := Default()
	a.SSO = SSOExternalOIDC // no issuer
	if err := a.Validate(); err == nil {
		t.Error("ExternalOIDC without issuer should fail validation")
	}
	a.OIDCIssuer = "https://idp.example.mil"
	if err := a.Validate(); err != nil {
		t.Errorf("ExternalOIDC with issuer should validate: %v", err)
	}
}

func TestValidate_BadEnums(t *testing.T) {
	a := Default()
	a.Posture = "Nope"
	a.Sizing = "Huge"
	if err := a.Validate(); err == nil {
		t.Error("invalid enums should fail validation")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	a := Default()
	a.Domain = "round.trip.test"
	path := filepath.Join(t.TempDir(), "answers.yaml")
	if err := a.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Domain != a.Domain || got.Posture != a.Posture {
		t.Errorf("round-trip mismatch: got %+v, want %+v", got, a)
	}
}

func TestHasService(t *testing.T) {
	a := Answers{Services: []string{"core-monitoring"}}
	if !a.HasService("core-monitoring") {
		t.Error("HasService should find a listed service")
	}
	if a.HasService("pgo") {
		t.Error("HasService should not find an unlisted service")
	}
}
