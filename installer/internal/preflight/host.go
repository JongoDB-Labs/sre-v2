package preflight

import (
	"bufio"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// Host is a snapshot of the facts the preflight checks reason over. Collecting
// facts into a struct keeps each check a pure function (easy to unit-test and to
// drive identically from the CLI and TUI).
type Host struct {
	// Arch is the GOARCH-style architecture string, e.g. "amd64" or "arm64".
	Arch string
	// NumCPU is the number of logical CPUs.
	NumCPU int
	// TotalRAMGiB is total physical memory in GiB.
	TotalRAMGiB float64
	// FreeDiskGiB is free space on the install target path, in GiB.
	FreeDiskGiB float64
	// KernelMajor and KernelMinor are the running kernel version components.
	KernelMajor int
	KernelMinor int
	// KernelRaw is the unparsed kernel release string (for display / when parsing fails).
	KernelRaw string
	// SwapKiB is total configured swap in KiB (0 means swap is off).
	SwapKiB int64
	// KmsgPresent is true when /dev/kmsg exists (Falco / ambient-mesh probes need it).
	KmsgPresent bool
	// Connected is true when the host appears to reach the internet (vs airgap).
	Connected bool
	// kernelParsed records whether KernelMajor/Minor were successfully parsed.
	kernelParsed bool
}

// kibPerGiB converts kibibytes to gibibytes.
const kibPerGiB = 1024 * 1024

// Collect gathers host facts for the current machine. Anything it cannot read is
// left at its zero value, and the dependent check downgrades to WARN rather than
// failing the whole run (e.g. on a dev laptop that is not the install target).
func Collect() Host {
	h := Host{
		Arch:        runtime.GOARCH,
		NumCPU:      runtime.NumCPU(),
		KmsgPresent: fileExists("/dev/kmsg"),
		Connected:   detectConnectivity(),
	}
	h.TotalRAMGiB, h.SwapKiB = readMemInfo()
	h.FreeDiskGiB = readFreeDiskGiB(installTargetPath())
	h.KernelMajor, h.KernelMinor, h.KernelRaw, h.kernelParsed = readKernel()
	return h
}

// installTargetPath is the path whose free space matters for the install. The
// in-cluster registry and PVs land under the data root; "/" is a safe proxy.
func installTargetPath() string { return "/" }

// fileExists reports whether path exists.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// readMemInfo parses /proc/meminfo for total RAM (GiB) and swap (KiB). On
// non-Linux hosts (e.g. a macOS dev box) /proc is absent, so it returns zeros
// and the dependent checks downgrade to WARN.
func readMemInfo() (ramGiB float64, swapKiB int64) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		kib, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			ramGiB = float64(kib) / kibPerGiB
		case "SwapTotal:":
			swapKiB = kib
		}
	}
	return ramGiB, swapKiB
}

// readFreeDiskGiB returns free space at path in GiB using statfs. It returns 0
// when statfs is unavailable or fails.
func readFreeDiskGiB(path string) float64 {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0
	}
	return float64(st.Bavail) * float64(st.Bsize) / (1024 * 1024 * 1024)
}

// readKernel reads the running kernel release from /proc/sys/kernel/osrelease and
// parses its leading major.minor. The boolean reports whether parsing succeeded.
func readKernel() (major, minor int, raw string, ok bool) {
	data, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		return 0, 0, "", false
	}
	raw = strings.TrimSpace(string(data))
	maj, min, ok := parseKernel(raw)
	return maj, min, raw, ok
}

// parseKernel extracts the leading "major.minor" from a kernel release string
// such as "6.8.0-31-generic". It is split out as a pure helper for testing.
func parseKernel(raw string) (major, minor int, ok bool) {
	parts := strings.SplitN(raw, ".", 3)
	if len(parts) < 2 {
		return 0, 0, false
	}
	maj, err1 := strconv.Atoi(parts[0])
	minStr := parts[1]
	// Trim any trailing non-digit suffix on the minor component.
	for i, r := range minStr {
		if r < '0' || r > '9' {
			minStr = minStr[:i]
			break
		}
	}
	min, err2 := strconv.Atoi(minStr)
	if err1 != nil || err2 != nil || minStr == "" {
		return 0, 0, false
	}
	return maj, min, true
}

// detectConnectivity makes a best-effort guess at whether the host can reach the
// internet, to distinguish a connected install from an airgap one. It is a hint,
// not a gate: the result only flips the upstream-vs-registry1 default reasoning.
func detectConnectivity() bool {
	// A real implementation would dial a known endpoint with a short timeout.
	// Stubbed for the skeleton: assume connected so the default flow is the lab
	// path; the airgap path is selected explicitly via posture/flavor.
	return true
}
