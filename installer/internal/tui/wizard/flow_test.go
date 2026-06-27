package wizard

import (
	"reflect"
	"testing"

	"github.com/JongoDB-Labs/sre-v2/installer/internal/catalog"
	"github.com/JongoDB-Labs/sre-v2/installer/internal/config"
)

func testCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	c, err := catalog.Load()
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	return c
}

func TestNewFlow_MatchesDefaults(t *testing.T) {
	f := NewFlow(testCatalog(t))
	got := f.Answers()
	want := config.Default()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("default Answers mismatch:\n got %+v\nwant %+v", got, want)
	}
}

func TestFlow_SettersPopulateAnswers(t *testing.T) {
	f := NewFlow(testCatalog(t))
	f.SetPosture(config.PostureDoD)
	f.SetSizing(config.SizingLarge)
	f.SetDomain("  example.gov  ") // trimmed
	if a := f.Answers(); a.Posture != config.PostureDoD || a.Sizing != config.SizingLarge || a.Domain != "example.gov" {
		t.Fatalf("setters not applied: %+v", f.Answers())
	}
}

func TestFlow_ToggleService(t *testing.T) {
	f := NewFlow(testCatalog(t))
	// minio is optional and NOT default-selected.
	if f.ServiceChecked("minio") {
		t.Fatal("minio should start unchecked")
	}
	f.ToggleService("minio")
	f.ToggleService("core-monitoring") // default-on → off
	a := f.Answers()
	if !a.HasService("minio") {
		t.Errorf("minio should be selected after toggle; got %v", a.Services)
	}
	if a.HasService("core-monitoring") {
		t.Errorf("core-monitoring should be deselected; got %v", a.Services)
	}
	// Services are emitted in catalog (deploy) order: identity, runtime-security, minio.
	want := []string{"core-identity-authorization", "core-runtime-security", "minio"}
	if !reflect.DeepEqual(a.Services, want) {
		t.Errorf("service order = %v, want %v", a.Services, want)
	}
}

func TestFlow_ToggleService_IgnoresRequired(t *testing.T) {
	f := NewFlow(testCatalog(t))
	f.ToggleService("pgo") // required → no-op
	if f.ServiceChecked("pgo") {
		t.Fatal("required service must not become a toggleable selection")
	}
}

func TestFlow_SSO_ConditionalIssuer(t *testing.T) {
	f := NewFlow(testCatalog(t))
	f.SetSSO(config.SSOExternalOIDC)
	if !f.NeedsOIDCIssuer() {
		t.Fatal("ExternalOIDC must require an issuer screen")
	}
	if err := f.Validate(); err == nil {
		t.Fatal("ExternalOIDC without an issuer should fail validation")
	}
	f.SetOIDCIssuer("https://idp.example.gov/realms/x")
	if err := f.Validate(); err != nil {
		t.Fatalf("ExternalOIDC + issuer should validate: %v", err)
	}
	// Switching away clears the stale issuer.
	f.SetSSO(config.SSOKeycloak)
	if f.NeedsOIDCIssuer() || f.Answers().OIDCIssuer != "" {
		t.Fatalf("switching away from ExternalOIDC must clear the issuer: %+v", f.Answers())
	}
}

func TestFlow_Secrets_ConditionalAgeKey(t *testing.T) {
	f := NewFlow(testCatalog(t))
	if !f.NeedsAgeKey() { // SOPSAge is the default
		t.Fatal("SOPSAge default must request an age key screen")
	}
	f.SetAgePublicKey("age1exampledonotusexxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxq0")
	f.SetSecrets(config.SecretsExternal)
	if f.NeedsAgeKey() || f.Answers().AgePublicKey != "" {
		t.Fatalf("switching to External must clear the age key: %+v", f.Answers())
	}
}
