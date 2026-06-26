package preflight

import "testing"

// goodHost returns a Host that passes every check.
func goodHost() Host {
	return Host{
		Arch:         "amd64",
		NumCPU:       16,
		TotalRAMGiB:  64,
		FreeDiskGiB:  200,
		KernelMajor:  6,
		KernelMinor:  8,
		KernelRaw:    "6.8.0-31-generic",
		SwapKiB:      0,
		KmsgPresent:  true,
		Connected:    true,
		kernelParsed: true,
	}
}

func TestRunWith_GoodHostPasses(t *testing.T) {
	r := RunWith(goodHost())
	if !r.OK() {
		t.Fatalf("expected OK host, got %d fails: %+v", r.Fails(), r.Results)
	}
	if r.Fails() != 0 || r.Warns() != 0 {
		t.Errorf("expected all PASS, got %d pass / %d warn / %d fail", r.Passes(), r.Warns(), r.Fails())
	}
}

func TestCheckArch(t *testing.T) {
	if got := checkArch(Host{Arch: "amd64"}); got.Status != StatusPass {
		t.Errorf("amd64: want PASS, got %s", got.Status)
	}
	if got := checkArch(Host{Arch: "arm64"}); got.Status != StatusFail {
		t.Errorf("arm64: want FAIL, got %s", got.Status)
	}
}

func TestCheckCPU(t *testing.T) {
	cases := []struct {
		cpu  int
		want Status
	}{
		{2, StatusFail},
		{8, StatusWarn},
		{12, StatusPass},
	}
	for _, c := range cases {
		if got := checkCPU(Host{NumCPU: c.cpu}); got.Status != c.want {
			t.Errorf("cpu=%d: want %s, got %s", c.cpu, c.want, got.Status)
		}
	}
}

func TestCheckKernel(t *testing.T) {
	old := Host{KernelMajor: 5, KernelMinor: 4, kernelParsed: true}
	if got := checkKernel(old); got.Status != StatusFail {
		t.Errorf("5.4: want FAIL, got %s", got.Status)
	}
	ok := Host{KernelMajor: 5, KernelMinor: 8, kernelParsed: true}
	if got := checkKernel(ok); got.Status != StatusPass {
		t.Errorf("5.8: want PASS, got %s", got.Status)
	}
	unknown := Host{kernelParsed: false}
	if got := checkKernel(unknown); got.Status != StatusWarn {
		t.Errorf("unknown: want WARN, got %s", got.Status)
	}
}

func TestCheckSwapAndKmsg(t *testing.T) {
	if got := checkSwap(Host{SwapKiB: 1024}); got.Status != StatusFail {
		t.Errorf("swap on: want FAIL, got %s", got.Status)
	}
	if got := checkKmsg(Host{KmsgPresent: false}); got.Status != StatusFail {
		t.Errorf("no kmsg: want FAIL, got %s", got.Status)
	}
}

func TestParseKernel(t *testing.T) {
	cases := []struct {
		raw      string
		maj, min int
		ok       bool
	}{
		{"6.8.0-31-generic", 6, 8, true},
		{"5.15.0", 5, 15, true},
		{"6.8", 6, 8, true},
		{"garbage", 0, 0, false},
	}
	for _, c := range cases {
		maj, min, ok := parseKernel(c.raw)
		if maj != c.maj || min != c.min || ok != c.ok {
			t.Errorf("parseKernel(%q) = (%d,%d,%t), want (%d,%d,%t)", c.raw, maj, min, ok, c.maj, c.min, c.ok)
		}
	}
}
