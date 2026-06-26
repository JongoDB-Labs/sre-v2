package preflight

import "fmt"

// Thresholds derived from docs/platform-runbook.md T0 "Host prerequisites":
//   - amd64 only for the substrate today.
//   - >=4 vCPU / 16 GiB runs app + slim Core; full UDS Core wants 12+ vCPU / 32+ GiB.
//   - ~100 GiB disk.
//   - kernel >=5.8 (Modern-eBPF probes for the ambient mesh + Falco).
const (
	minCPU         = 4
	recommendedCPU = 12
	minRAMGiB      = 16.0
	recRAMGiB      = 32.0
	minDiskGiB     = 100.0
	kernelMinMajor = 5
	kernelMinMinor = 8
)

// Check is a pure host-readiness check: given the collected facts it returns a
// single Result. Checks never perform I/O, so they are trivially testable and
// run identically from the CLI and the TUI.
type Check func(Host) Result

// Checks returns the ordered set of preflight checks.
func Checks() []Check {
	return []Check{
		checkArch,
		checkCPU,
		checkRAM,
		checkDisk,
		checkKernel,
		checkSwap,
		checkKmsg,
		checkConnectivity,
	}
}

// checkArch verifies the host is amd64 — the only architecture the substrate
// bundle currently targets.
func checkArch(h Host) Result {
	if h.Arch == "amd64" {
		return pass("architecture", h.Arch)
	}
	return fail("architecture", h.Arch,
		"substrate bundle is amd64-only; run the installer on an amd64/x86_64 host")
}

// checkCPU verifies vCPU count against the slim floor and the full-Core target.
func checkCPU(h Host) Result {
	detail := fmt.Sprintf("%d vCPU", h.NumCPU)
	switch {
	case h.NumCPU < minCPU:
		return fail("cpu", detail,
			fmt.Sprintf("need >=%d vCPU for app + slim Core (12+ for full UDS Core)", minCPU))
	case h.NumCPU < recommendedCPU:
		return warn("cpu", detail,
			fmt.Sprintf("full UDS Core wants %d+ vCPU; %d runs the slim core only", recommendedCPU, h.NumCPU))
	default:
		return pass("cpu", detail)
	}
}

// checkRAM verifies total RAM against the slim floor and the full-Core target.
func checkRAM(h Host) Result {
	if h.TotalRAMGiB == 0 {
		return warn("memory", "unknown",
			"could not read /proc/meminfo; ensure >=16 GiB (32+ GiB for full UDS Core)")
	}
	detail := fmt.Sprintf("%.1f GiB", h.TotalRAMGiB)
	switch {
	case h.TotalRAMGiB < minRAMGiB:
		return fail("memory", detail,
			fmt.Sprintf("need >=%.0f GiB for app + slim Core (32+ for full UDS Core)", minRAMGiB))
	case h.TotalRAMGiB < recRAMGiB:
		return warn("memory", detail,
			fmt.Sprintf("full UDS Core wants %.0f+ GiB; %.1f runs the slim core only", recRAMGiB, h.TotalRAMGiB))
	default:
		return pass("memory", detail)
	}
}

// checkDisk verifies free disk on the install target against the ~100 GiB floor.
func checkDisk(h Host) Result {
	if h.FreeDiskGiB == 0 {
		return warn("disk", "unknown",
			"could not statfs the install path; ensure ~100 GiB free")
	}
	detail := fmt.Sprintf("%.0f GiB free", h.FreeDiskGiB)
	if h.FreeDiskGiB < minDiskGiB {
		return warn("disk", detail,
			fmt.Sprintf("recommend ~%.0f GiB free for images + PVs; only %.0f GiB available", minDiskGiB, h.FreeDiskGiB))
	}
	return pass("disk", detail)
}

// checkKernel verifies the running kernel is >=5.8 for the Modern-eBPF probes
// the ambient mesh and Falco rely on.
func checkKernel(h Host) Result {
	if !h.kernelParsed {
		return warn("kernel", "unknown",
			"could not read kernel version; ensure kernel >=5.8 for ambient mesh + Falco eBPF")
	}
	detail := fmt.Sprintf("kernel %d.%d", h.KernelMajor, h.KernelMinor)
	if h.KernelMajor < kernelMinMajor ||
		(h.KernelMajor == kernelMinMajor && h.KernelMinor < kernelMinMinor) {
		return fail("kernel", detail,
			"kernel >=5.8 required for UDS ambient mesh + Falco Modern-eBPF probes; upgrade the host kernel")
	}
	return pass("kernel", detail)
}

// checkSwap verifies swap is off — a Kubernetes node prerequisite.
func checkSwap(h Host) Result {
	if h.SwapKiB > 0 {
		return fail("swap", fmt.Sprintf("%d KiB", h.SwapKiB),
			"disable swap for Kubernetes: `sudo swapoff -a` and remove it from /etc/fstab")
	}
	return pass("swap", "off")
}

// checkKmsg verifies /dev/kmsg is present — Falco and the ambient mesh read it.
func checkKmsg(h Host) Result {
	if h.KmsgPresent {
		return pass("/dev/kmsg", "present")
	}
	return fail("/dev/kmsg", "missing",
		"/dev/kmsg absent (common in unprivileged LXC) — use a VM or privileged container")
}

// checkConnectivity reports the connected-vs-airgap posture. Either is valid, so
// this is informational: it only nudges the upstream-vs-registry1 flavor default.
func checkConnectivity(h Host) Result {
	if h.Connected {
		return pass("connectivity", "connected (upstream images reachable)")
	}
	return warn("connectivity", "airgap",
		"airgap detected — use the DoD posture (registry1/Iron Bank) and a Zarf-seeded registry")
}

// Run collects host facts and evaluates every check, returning the aggregate
// Report. It is the single entry point shared by the CLI and the TUI.
func Run() Report {
	return RunWith(Collect())
}

// RunWith evaluates every check against the supplied host facts. Tests inject a
// crafted Host; production passes Collect().
func RunWith(h Host) Report {
	checks := Checks()
	results := make([]Result, 0, len(checks))
	for _, c := range checks {
		results = append(results, c(h))
	}
	return Report{Results: results}
}
