# srectl Day-2 console — Phase 7 slice 3 (enrich-compliance: image-signing posture) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Add a READ-ONLY **software-supply-chain posture check** to the compliance view (spec §7 P7): verify that the operator's running images are properly **cosign-signed**, and roll the result into the ConMon posture (PASS/WARN/FAIL/—). Config-driven + app-agnostic: the operator declares the expected signer (identity-regexp + OIDC issuer) and, for airgap, a bundle + trusted-root — the check verifies only images matching the operator's configured registry prefix.

**Architecture:** PURE pieces — `RunningImages(podsJSON, prefix)` (distinct images under the operator's prefix), `cosignVerifyArgs(image, cfg)` (the `cosign verify` args; keyless online, or `--bundle/--trusted-root` for airgap), `SigningCheck(results)` (→ PostureCheck) — all unit-tested. A thin `Cosign` exec-wrapper runs `cosign verify` per image (bounded, off-UI). `VerifyConfig` loads from env (`SRECTL_VERIFY_*`). The check joins `gatherPostureChecks` (so it shows in the view AND the export), best-effort: not-configured / no-matching-images → `—`; verify error → the per-image failure surfaces as FAIL/WARN. Read-only; nothing mutates.

**Tech Stack:** Go 1.25, `cosign` (present on the lab), kubectl (`get pods -A -o json`), the P7-compliance posture types.

## Global Constraints

- **Module / cwd:** `github.com/JongoDB-Labs/sre-v2/installer`, go 1.25. From `/Users/JonWFH/jondev/sre-v2-wt-mon/installer` on branch **`feat/srectl-day2-signing`** (off main, which has the merged compliance + export). The controller creates the branch before Task 1.
- **READ-ONLY:** the whole slice only reads (get pods, `cosign verify`). No mutation, no action, no audit entry. The check has no `a`-action. **It must NOT read/extract any Kubernetes Secret or pull token** — verification relies on the operator having made the images public OR run `cosign login` themselves; the console NEVER touches registry credentials.
- **App-agnostic + config-driven:** the expected signer + scope come from env (`SRECTL_VERIFY_IDENTITY` regexp, `SRECTL_VERIFY_ISSUER`, `SRECTL_VERIFY_IMAGE_PREFIX`, optional `SRECTL_VERIFY_BUNDLE` + `SRECTL_VERIFY_TRUSTED_ROOT` for airgap). No hardcoded identity/registry/namespace. If identity+issuer+prefix are not all set → the check is `—` "signing verification not configured", NOT FAIL.
- **Both verify modes (user: "1 and 3"):** keyless-online = `cosign verify <img> --certificate-identity-regexp <id> --certificate-oidc-issuer <issuer>`; airgap = the same PLUS `--bundle <b> --trusted-root <r>` when both are configured (NOTE: `--offline` is deprecated in current cosign — do NOT use it; the modern airgap path is `--bundle`+`--trusted-root`).
- **Exec-wrapper rule:** `cosign` via a fake-backed interface; the arg-builders + RunningImages + SigningCheck are PURE + unit-tested.
- **Anti-freeze:** the per-image `cosign verify` calls run inside `gatherPostureChecks` (already off the UI goroutine via the background `tableView.fetch`); each call timeout-bounded.
- **Lab facts (recon 2026-06-29):** `cosign` at `/usr/local/bin/cosign`; running image `ghcr.io/jongodb-labs/cosmos-v2@sha256:…` (was private → 401 until the operator makes it public or logs in). The exact signer identity/issuer are discovered in the Task-3 smoke (once the image is reachable).
- **Commits:** noreply.

---

## File Structure

**Create:**
- `installer/internal/tui/monitor/data/signing.go` (package `data`) — `VerifyConfig` + `LoadVerifyConfig` + `RunningImages` + `cosignVerifyArgs` + `SigningCheck` + a `Cosign` exec-wrapper (`VerifyImage`).
- `installer/internal/tui/monitor/data/signing_test.go`

**Modify:**
- `installer/internal/tui/monitor/monitor.go` — add the signing check to `gatherPostureChecks` (gather running images → verify each → SigningCheck), best-effort.

---

## Task 1: data/signing.go — config + image extraction + verify args + check (PURE + wrapper)

**Files:**
- Create: `installer/internal/tui/monitor/data/signing.go`, `data/signing_test.go`

**Interfaces:**
- Produces: `type VerifyConfig struct{ IdentityRegexp, Issuer, ImagePrefix, Bundle, TrustedRoot string }`; `func LoadVerifyConfig() VerifyConfig` (from env); `func (c VerifyConfig) Configured() bool`; `func RunningImages(podsJSON []byte, prefix string) []string`; `func cosignVerifyArgs(image string, c VerifyConfig) []string`; `type ImageResult struct{ Image string; OK bool; Err string }`; `func SigningCheck(results []ImageResult, configured bool) PostureCheck`; a `Cosign` interface `VerifyImage(image string, c VerifyConfig) error` + `execCosign` impl.

- [ ] **Step 1: Write the failing test** — `data/signing_test.go`:

```go
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
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && go test ./internal/tui/monitor/data/ -run 'TestRunningImages|TestCosignVerifyArgs|TestConfigured|TestSigningCheck' -v`
Expected: FAIL — `undefined: RunningImages`.

- [ ] **Step 3: Implement** — create `data/signing.go`:

```go
package data

import (
	"context"
	"fmt"
	"encoding/json"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

const cosignTimeout = 20 * time.Second

// VerifyConfig declares the expected image signer + scope (operator-supplied via env).
type VerifyConfig struct {
	IdentityRegexp string // --certificate-identity-regexp
	Issuer         string // --certificate-oidc-issuer
	ImagePrefix    string // only verify images under this registry/repo prefix
	Bundle         string // airgap: --bundle (optional)
	TrustedRoot    string // airgap: --trusted-root (optional)
}

// LoadVerifyConfig reads the SRECTL_VERIFY_* environment.
func LoadVerifyConfig() VerifyConfig {
	return VerifyConfig{
		IdentityRegexp: os.Getenv("SRECTL_VERIFY_IDENTITY"),
		Issuer:         os.Getenv("SRECTL_VERIFY_ISSUER"),
		ImagePrefix:    os.Getenv("SRECTL_VERIFY_IMAGE_PREFIX"),
		Bundle:         os.Getenv("SRECTL_VERIFY_BUNDLE"),
		TrustedRoot:    os.Getenv("SRECTL_VERIFY_TRUSTED_ROOT"),
	}
}

// Configured reports whether enough is set to attempt verification.
func (c VerifyConfig) Configured() bool {
	return c.IdentityRegexp != "" && c.Issuer != "" && c.ImagePrefix != ""
}

// RunningImages returns the distinct container/initContainer images (across all pods)
// whose ref starts with prefix, sorted.
func RunningImages(podsJSON []byte, prefix string) []string {
	var list struct {
		Items []struct {
			Spec struct {
				Containers     []struct{ Image string } `json:"containers"`
				InitContainers []struct{ Image string } `json:"initContainers"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.Unmarshal(podsJSON, &list); err != nil {
		return nil
	}
	seen := map[string]bool{}
	for _, it := range list.Items {
		for _, cs := range [][]struct{ Image string }{it.Spec.Containers, it.Spec.InitContainers} {
			for _, c := range cs {
				if strings.HasPrefix(c.Image, prefix) {
					seen[c.Image] = true
				}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for img := range seen {
		out = append(out, img)
	}
	sort.Strings(out)
	return out
}

// cosignVerifyArgs builds the `cosign verify` args (keyless online; +bundle/trusted-root
// for airgap). NOTE: --offline is deprecated in current cosign; airgap uses --bundle.
func cosignVerifyArgs(image string, c VerifyConfig) []string {
	args := []string{"verify", image,
		"--certificate-identity-regexp", c.IdentityRegexp,
		"--certificate-oidc-issuer", c.Issuer}
	if c.Bundle != "" && c.TrustedRoot != "" {
		args = append(args, "--bundle", c.Bundle, "--trusted-root", c.TrustedRoot)
	}
	return args
}

// ImageResult is one image's verification outcome.
type ImageResult struct {
	Image string
	OK    bool
	Err   string
}

// SigningCheck rolls per-image results into a posture line.
func SigningCheck(results []ImageResult, configured bool) PostureCheck {
	const name = "Image signing"
	if !configured {
		return PostureCheck{name, PostureNA, "not configured (set SRECTL_VERIFY_IDENTITY/ISSUER/IMAGE_PREFIX)"}
	}
	if len(results) == 0 {
		return PostureCheck{name, PostureNA, "no images under the configured prefix"}
	}
	var bad []string
	for _, r := range results {
		if !r.OK {
			bad = append(bad, r.Image)
		}
	}
	if len(bad) > 0 {
		return PostureCheck{name, PostureFAIL, fmt.Sprintf("%d/%d unverified (e.g. %s)", len(bad), len(results), short(bad[0]))}
	}
	return PostureCheck{name, PosturePASS, fmt.Sprintf("%d image(s) cosign-verified", len(results))}
}

func short(image string) string {
	if i := strings.LastIndex(image, "/"); i >= 0 && i+1 < len(image) {
		return image[i+1:]
	}
	return image
}

// Cosign verifies an image's signature.
type Cosign interface {
	VerifyImage(image string, c VerifyConfig) error
}

type execCosign struct{}

// NewCosign returns the real cosign-backed verifier.
func NewCosign() Cosign { return execCosign{} }

func (execCosign) VerifyImage(image string, c VerifyConfig) error {
	ctx, cancel := context.WithTimeout(context.Background(), cosignTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "cosign", cosignVerifyArgs(image, c)...).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if len(msg) > 120 {
			msg = msg[:120]
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/tui/monitor/data/ -v && gofmt -l internal/tui/monitor/data/signing.go internal/tui/monitor/data/signing_test.go`
Expected: PASS (images/args/configured/check + all existing data tests); gofmt clean.

- [ ] **Step 5: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/data/signing.go installer/internal/tui/monitor/data/signing_test.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): image-signing posture (cosign verify, config-driven, keyless+airgap)"
```

---

## Task 2: monitor — add the signing check to gatherPostureChecks

**Files:**
- Modify: `installer/internal/tui/monitor/monitor.go`

**Interfaces:**
- Consumes: `data.LoadVerifyConfig`/`RunningImages`/`SigningCheck`/`ImageResult`/`Cosign`, `m.res.Get("pods")` (or the existing pods fetch), the monitor's `cosign` field.

- [ ] **Step 1: Add a Cosign verifier to the monitor + the signing gather**

Add a `cosign data.Cosign` field to the `monitor` struct, initialized `cosign: data.NewCosign()` in `Run`. Append to `gatherPostureChecks()` (after the falco check), best-effort:

```go
	// Software supply-chain: image signing (config-driven, off-UI, read-only).
	cfg := data.LoadVerifyConfig()
	if !cfg.Configured() {
		checks = append(checks, data.SigningCheck(nil, false))
	} else if pods, err := m.res.Get("pods", "-A"); err == nil {
		imgs := data.RunningImages(pods, cfg.ImagePrefix)
		var results []data.ImageResult
		for _, img := range imgs {
			r := data.ImageResult{Image: img, OK: true}
			if verr := m.cosign.VerifyImage(img, cfg); verr != nil {
				r.OK, r.Err = false, verr.Error()
			}
			results = append(results, r)
		}
		checks = append(checks, data.SigningCheck(results, true))
	} else {
		checks = append(checks, data.PostureCheck{Name: "Image signing", Status: data.PostureWARN, Detail: "unavailable: " + err.Error()})
	}
```

(Read how `fetchPods`/the resource fetch calls `m.res.Get` to match the exact signature — `Get(resource string, extraArgs ...string)` returns `([]byte, error)`. If the signature differs, adapt the `m.res.Get("pods", "-A")` call to the real one that returns all-namespace pods JSON.)

- [ ] **Step 2: Build + suite + compile-smoke**

Run: `cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && go build ./... && go vet ./internal/tui/monitor/... && gofmt -l internal/tui/monitor/monitor.go && go test ./... -count=1 2>&1 | tail -4 && go run ./cmd/srectl monitor --help >/dev/null && echo HELP_OK`
Expected: build clean; gofmt clean; suite green.

- [ ] **Step 3: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/monitor.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): wire the image-signing check into the ConMon posture gather"
```

---

## Task 3: Lab smoke (controller-driven) — REQUIRES the operator to have made the image public (or `cosign login`)

> **Gate:** this smoke needs the running image's signature to be reachable — i.e. the operator made `ghcr.io/jongodb-labs/cosmos-v2` public OR ran `cosign login ghcr.io` on the lab. The controller confirms reachability first; if still 401, the smoke records "blocked on registry visibility" and the slice lands with the unit-tested code + a degraded-path smoke (NA/WARN), to be re-smoked for a green PASS once public.

- [ ] **Step 1: Discover the real signer identity (once the image is reachable)**

```bash
ssh cosmos@cosmos-ssh.fightingsmartcyber.com 'IMG="ghcr.io/jongodb-labs/cosmos-v2@sha256:a2c7fa0bece331bb12ec547ac73439e871daff3b5544c677c4bbc567c71ec467"; cosign verify "$IMG" --certificate-identity-regexp ".*" --certificate-oidc-issuer-regexp ".*" 2>&1 | python3 -c "import sys,json; t=sys.stdin.read(); a=json.loads(t); c=a[0][\"optional\"]; print(\"identity:\",c.get(\"Subject\")); print(\"issuer:\",c.get(\"Issuer\"))" 2>/dev/null || echo "still unreachable (image not public / not logged in)"'
```
Record the discovered `identity` + `issuer` (the GHA release-workflow identity + `https://token.actions.githubusercontent.com`) — these become the `SRECTL_VERIFY_*` smoke values.

- [ ] **Step 2: Cross-compile + deliver + drive with the config set**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && GOOS=linux GOARCH=amd64 go build -o /tmp/srectl ./cmd/srectl
cat /tmp/srectl | ssh cosmos@cosmos-ssh.fightingsmartcyber.com 'cat > /tmp/srectl && chmod +x /tmp/srectl'
ssh cosmos@cosmos-ssh.fightingsmartcyber.com bash -s <<EOF
export SRECTL_VERIFY_IDENTITY='<discovered identity regexp>'
export SRECTL_VERIFY_ISSUER='https://token.actions.githubusercontent.com'
export SRECTL_VERIFY_IMAGE_PREFIX='ghcr.io/jongodb-labs/'
tmux kill-server 2>/dev/null || true; sleep 0.5
tmux new-session -d -s mon -x 170 -y 48; sleep 0.5
tmux send-keys -t mon '/tmp/srectl monitor' Enter; sleep 4
tmux send-keys -t mon ':'; sleep 0.5; tmux send-keys -t mon 'compliance' Enter; sleep 4
echo "=== COMPLIANCE (now incl. Image signing) ==="; tmux capture-pane -t mon -p | sed -n '2,9p'
tmux send-keys -t mon q; sleep 1; tmux kill-server 2>/dev/null || true
EOF
```
Expected: the COMPLIANCE view shows a 4th row **Image signing = PASS** ("N image(s) cosign-verified") in green once the image is public + the identity/issuer are set. Without the env (default run) it shows **Image signing = —** "not configured". (Record the outcome + the discovered identity in the ledger.)

---

## Self-Review

**1. Spec coverage (P7 enrich — supply chain):** image-signing posture (cosign verify, keyless online + airgap bundle/trusted-root, config-driven) → Tasks 1+2. Vuln (no lab scanner) + patch-level → deferred. SLSA attestation (`verify-attestation`) → a follow-on (same wrapper shape).

**2. Placeholder scan:** Task 1 ships the full pure pieces (images/args/config/check) + the cosign wrapper, 5 tests. Task 2 wires it into the shared gather (best-effort). Task 3 proves it live (gated on the operator making the image reachable; degraded-path otherwise). No TODO/"similar to".

**3. Type consistency:** `VerifyConfig`/`RunningImages`/`cosignVerifyArgs`/`SigningCheck`/`ImageResult`/`Cosign` (T1) consumed by `gatherPostureChecks` (T2). `SigningCheck` → a `PostureCheck` that flows into BOTH the compliance view AND the export artifact (it's just another check). READ-ONLY: get pods + cosign verify; NO Secret/credential access (relies on public images or operator `cosign login`). App-agnostic: env-config, `—` when unconfigured. Both verify modes in `cosignVerifyArgs` (keyless; +bundle/trusted-root, NOT the deprecated --offline).
