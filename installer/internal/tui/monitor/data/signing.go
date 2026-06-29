package data

import (
	"context"
	"encoding/json"
	"fmt"
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
