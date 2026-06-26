// Package render turns the installer answer model into the two re-runnable,
// git-committable files the deploy step consumes:
//
//   - uds-config.yaml     — UDS bundle variables (image flavor, which packages,
//     sizing knobs, SSO mode, domain).
//   - values.overlay.yaml — Helm value overlay (sizing -> replicas/resources/PG
//     instances/storage; posture -> FIPS/netpol/retention).
//
// The posture->flavor and sizing->resources mappings live here as explicit
// tables so the policy is reviewable in one place.
package render

import "github.com/JongoDB-Labs/sre-v2/installer/internal/config"

// Flavor is the UDS image flavor a posture resolves to.
type Flavor string

const (
	// FlavorUpstream uses upstream community images (Baseline / lab).
	FlavorUpstream Flavor = "upstream"
	// FlavorRegistry1 uses Iron Bank hardened images (DoD / ATO).
	FlavorRegistry1 Flavor = "registry1"
)

// postureFlavor maps each posture to its UDS image flavor. Baseline tracks
// upstream; DoD swaps in the registry1 (Iron Bank) hardened images.
var postureFlavor = map[config.Posture]Flavor{
	config.PostureBaseline: FlavorUpstream,
	config.PostureDoD:      FlavorRegistry1,
}

// FlavorFor returns the image flavor for a posture, defaulting to upstream.
func FlavorFor(p config.Posture) Flavor {
	if f, ok := postureFlavor[p]; ok {
		return f
	}
	return FlavorUpstream
}

// PostureProfile captures the posture-driven hardening knobs that land in the
// values overlay (orthogonal to sizing).
type PostureProfile struct {
	// FIPS toggles FIPS-mode crypto across the substrate.
	FIPS bool
	// NetworkPolicy is the default-deny posture ("strict" hardens egress further).
	NetworkPolicy string
	// AuditRetentionDays is the audit/log retention floor.
	AuditRetentionDays int
}

// postureProfiles maps each posture to its hardening profile. The DoD retention
// floor (>=1095d) matches the gov audit retention floor in the runbook (SP3).
var postureProfiles = map[config.Posture]PostureProfile{
	config.PostureBaseline: {FIPS: false, NetworkPolicy: "default-deny", AuditRetentionDays: 30},
	config.PostureDoD:      {FIPS: true, NetworkPolicy: "strict", AuditRetentionDays: 1095},
}

// ProfileFor returns the hardening profile for a posture, defaulting to Baseline.
func ProfileFor(p config.Posture) PostureProfile {
	if prof, ok := postureProfiles[p]; ok {
		return prof
	}
	return postureProfiles[config.PostureBaseline]
}

// ResourceProfile captures the sizing-driven envelope that lands in the values
// overlay: workload replicas, per-pod requests/limits, Postgres instance count,
// and the default PV size.
type ResourceProfile struct {
	// Replicas is the default workload replica count.
	Replicas int
	// CPURequest / CPULimit are per-pod CPU bounds (Kubernetes quantity strings).
	CPURequest string
	CPULimit   string
	// MemoryRequest / MemoryLimit are per-pod memory bounds.
	MemoryRequest string
	MemoryLimit   string
	// PGInstances is the number of Postgres instances PGO runs (1 = single, 3 = HA).
	PGInstances int
	// StorageSize is the default PersistentVolume size.
	StorageSize string
}

// sizingProfiles maps each sizing tier to its resource envelope. Small is the
// single-node slim-Core lab; Medium is full UDS Core on a 12+ vCPU node; Large
// is an HA, production-shaped envelope.
var sizingProfiles = map[config.Sizing]ResourceProfile{
	config.SizingSmall: {
		Replicas:      1,
		CPURequest:    "100m",
		CPULimit:      "500m",
		MemoryRequest: "256Mi",
		MemoryLimit:   "1Gi",
		PGInstances:   1,
		StorageSize:   "10Gi",
	},
	config.SizingMedium: {
		Replicas:      2,
		CPURequest:    "250m",
		CPULimit:      "1",
		MemoryRequest: "512Mi",
		MemoryLimit:   "2Gi",
		PGInstances:   2,
		StorageSize:   "50Gi",
	},
	config.SizingLarge: {
		Replicas:      3,
		CPURequest:    "500m",
		CPULimit:      "2",
		MemoryRequest: "1Gi",
		MemoryLimit:   "4Gi",
		PGInstances:   3,
		StorageSize:   "100Gi",
	},
}

// ResourcesFor returns the resource profile for a sizing tier, defaulting to Small.
func ResourcesFor(s config.Sizing) ResourceProfile {
	if r, ok := sizingProfiles[s]; ok {
		return r
	}
	return sizingProfiles[config.SizingSmall]
}
