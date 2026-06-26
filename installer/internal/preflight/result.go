// Package preflight runs host readiness checks before a substrate install:
// architecture, CPU/RAM/disk, kernel version, swap, /dev/kmsg, and connected-vs
// -airgap detection. Each check is a pure function returning a Result (status +
// one-line remediation), and Run aggregates them so both the CLI table and the
// TUI wizard can present identical findings. Remediations reference the T0
// prerequisites and gotcha catalog in docs/platform-runbook.md.
package preflight

// Status is the outcome of a single preflight check.
type Status string

const (
	// StatusPass means the host satisfies the requirement.
	StatusPass Status = "PASS"
	// StatusWarn means the host is usable but sub-optimal or unverifiable.
	StatusWarn Status = "WARN"
	// StatusFail means the requirement is not met and install will likely fail.
	StatusFail Status = "FAIL"
)

// Result is the outcome of one check: its name, status, an observed detail, and
// a one-line remediation (empty when the check passes).
type Result struct {
	// Name is the short check label, e.g. "architecture" or "kernel".
	Name string
	// Status is PASS, WARN, or FAIL.
	Status Status
	// Detail is the observed value, e.g. "amd64" or "kernel 6.8".
	Detail string
	// Remediation is a one-line fix hint, set when Status is WARN or FAIL.
	Remediation string
}

// pass builds a passing Result.
func pass(name, detail string) Result {
	return Result{Name: name, Status: StatusPass, Detail: detail}
}

// warn builds a warning Result with a remediation hint.
func warn(name, detail, remediation string) Result {
	return Result{Name: name, Status: StatusWarn, Detail: detail, Remediation: remediation}
}

// fail builds a failing Result with a remediation hint.
func fail(name, detail, remediation string) Result {
	return Result{Name: name, Status: StatusFail, Detail: detail, Remediation: remediation}
}

// Report is the aggregate outcome of a preflight run.
type Report struct {
	// Results holds every check outcome, in check order.
	Results []Result
}

// counts returns the number of results in each status.
func (r Report) counts() (passes, warns, fails int) {
	for _, res := range r.Results {
		switch res.Status {
		case StatusPass:
			passes++
		case StatusWarn:
			warns++
		case StatusFail:
			fails++
		}
	}
	return
}

// Passes returns the number of passing checks.
func (r Report) Passes() int { p, _, _ := r.counts(); return p }

// Warns returns the number of warning checks.
func (r Report) Warns() int { _, w, _ := r.counts(); return w }

// Fails returns the number of failing checks.
func (r Report) Fails() int { _, _, f := r.counts(); return f }

// OK reports whether the host passed preflight (no failing checks). Warnings do
// not block; they are advisory.
func (r Report) OK() bool { return r.Fails() == 0 }
