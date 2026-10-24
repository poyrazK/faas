package sched_test

// adr: 1155 pins the admitted CPU quota the CPU saturation trigger measures against.

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/sched"
)

// ADR-1155: CPU saturation captures compare use with the sustained quota,
// never with a startup boost, and know nothing of unadmitted instances.
func TestSustainedCPUMillicores(t *testing.T) {
	t.Parallel()
	// Admit expires boosts against the wall clock, so the test does too.
	now := time.Now()
	ledger := sched.NewNodeLedger()
	steady := cpuAdmissionRequest("steady")
	steady.CPUMillicores = 500
	boosted := cpuAdmissionRequest("boosted")
	boosted.CPUMillicores = 250
	boosted.CPUStartupBoostMillicores = 1000
	boosted.CPUStartupBoostUntil = now.Add(time.Minute)
	for _, r := range []sched.Request{steady, boosted} {
		if err := ledger.Admit(r); err != nil {
			t.Fatalf("admit %s: %v", r.Instance, err)
		}
	}
	if got, ok := ledger.SustainedCPUMillicores("steady", now); !ok || got != 500 {
		t.Fatalf("steady quota = %d, %v; want 500", got, ok)
	}
	if _, ok := ledger.SustainedCPUMillicores("boosted", now); ok {
		t.Fatal("boosted instance reported a sustained quota during its boost")
	}
	if got, ok := ledger.SustainedCPUMillicores("boosted", now.Add(2*time.Minute)); !ok || got != 250 {
		t.Fatalf("boosted quota after the boost = %d, %v; want 250", got, ok)
	}
	if _, ok := ledger.SustainedCPUMillicores("missing", now); ok {
		t.Fatal("unadmitted instance reported a quota")
	}
	ledger.Release("steady")
	if _, ok := ledger.SustainedCPUMillicores("steady", now); ok {
		t.Fatal("released instance reported a quota")
	}
}
