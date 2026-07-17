package sched

// Property-based test for the admission ledger — the mechanised form of
// invariants §6.2-1 (per-app concurrency) and §6.2-2 (Σ(ram+8) ≤ 47,600 MB).
// CLAUDE.md: "Invariants — enforce with property-based tests, never delete."
//
// A native Go fuzz target drives a random sequence of Admit/BeginSnapshot/Release
// operations over a small fixed universe of apps and instances, then asserts —
// after EVERY operation — that the ledger's internal accounting is consistent
// with the ground truth recomputed from its live entries and that no invariant
// is ever breached. This is a white-box test (package sched) so it can read the
// unexported fields the public API only exposes as aggregates.
//
// Errors from the ledger are legal outcomes (a full box rejects a wake), so the
// operations ignore returned errors; the invariants must hold whether an op
// succeeded or was refused.

import (
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// propApp is one app in the fuzz universe, with a plan and the request shape the
// ledger will see. The RAM/vCPU/concurrency values are the plan maxima so the
// ledger's own clamping (Admit uses min(req, plan)) is a no-op and the expected
// concurrency ceiling is exactly conc.
type propApp struct {
	id   string
	plan api.Plan
	ram  int
	vcpu int
	conc int
}

var propApps = []propApp{
	{"free", api.PlanFree, 128, 2, 1},
	{"hobby", api.PlanHobby, 256, 2, 2},
	{"pro", api.PlanPro, 512, 2, 5},
	{"scale", api.PlanScale, 1024, 4, 20},
}

// instancesPerApp bounds the instance-id pool so BeginSnapshot/Release land on
// instances that plausibly exist. It is deliberately larger than any plan's
// concurrency cap so the fuzzer exercises the rejection path.
const instancesPerApp = 8

func FuzzLedgerInvariants(f *testing.F) {
	// Seeds: a simple grow, a churn pattern, and a burst that pushes toward the
	// ceiling. Each byte encodes one operation (decoded in decodeOp).
	f.Add([]byte{0x00, 0x04, 0x08, 0x0c})
	f.Add([]byte{0x00, 0x40, 0x80, 0x00, 0x40, 0x80})
	f.Add(make([]byte, 256)) // 256 admits of the same (free) instance → dedup + churn

	f.Fuzz(func(t *testing.T, ops []byte) {
		l := NewLedger()
		for i, b := range ops {
			applyOp(l, b)
			checkLedgerInvariants(t, l, i)
		}
	})
}

// applyOp decodes one byte into an operation and applies it, ignoring the
// (legal) error return. Byte layout:
//
//	bit 0-1: action (0,3 = Admit; 1 = BeginSnapshot; 2 = Release)
//	bit 2-3: app index into propApps
//	bit 4-6: instance index within the app (0..7)
func applyOp(l *Ledger, b byte) {
	app := propApps[(b>>2)&0x03]
	inst := fmt.Sprintf("%s-%d", app.id, int(b>>4)%instancesPerApp)
	switch b & 0x03 {
	case 1:
		l.BeginSnapshot(inst)
	case 2:
		l.Release(inst)
	default: // 0 and 3 both Admit — weight admission higher than teardown
		_ = l.Admit(Request{
			Instance:       inst,
			AppID:          app.id,
			Plan:           app.plan,
			RAMMB:          app.ram,
			VCPU:           app.vcpu,
			MaxConcurrency: app.conc,
		})
	}
}

// checkLedgerInvariants recomputes ground truth from l.entries and asserts the
// cached aggregates match, then checks the hard invariants. Single-goroutine
// test, so reading unexported fields without the mutex is safe.
func checkLedgerInvariants(t *testing.T, l *Ledger, step int) {
	t.Helper()

	var wantRAM, wantVCPU int
	wantConc := map[string]int{}
	for _, e := range l.entries {
		wantRAM += e.admissionMB
		wantVCPU += e.vcpu
		if e.countsConc {
			wantConc[e.appID]++
		}
	}

	// Cached aggregates must equal the recomputed truth (no drift on any path).
	if l.residentRAM != wantRAM {
		t.Fatalf("step %d: residentRAM=%d, recomputed=%d", step, l.residentRAM, wantRAM)
	}
	if l.usedVCPU != wantVCPU {
		t.Fatalf("step %d: usedVCPU=%d, recomputed=%d", step, l.usedVCPU, wantVCPU)
	}
	// perApp must have no stale/zero/negative entries and match the truth.
	for app, c := range l.perApp {
		if c <= 0 {
			t.Fatalf("step %d: perApp[%q]=%d — stale/negative entry left behind", step, app, c)
		}
		if wantConc[app] != c {
			t.Fatalf("step %d: perApp[%q]=%d, recomputed=%d", step, app, c, wantConc[app])
		}
	}
	for app, c := range wantConc {
		if l.perApp[app] != c {
			t.Fatalf("step %d: perApp missing app %q (have %d, want %d)", step, app, l.perApp[app], c)
		}
	}

	// Hard invariants — these are the product.
	if l.residentRAM < 0 || l.usedVCPU < 0 {
		t.Fatalf("step %d: negative accounting: ram=%d vcpu=%d", step, l.residentRAM, l.usedVCPU)
	}
	if l.residentRAM > api.RAMAdmissionCeilingMB { // §6.2-2
		t.Fatalf("step %d: residentRAM=%d breached admission ceiling %d",
			step, l.residentRAM, api.RAMAdmissionCeilingMB)
	}
	if l.usedVCPU > api.VCPUSlots {
		t.Fatalf("step %d: usedVCPU=%d exceeded %d vCPU slots", step, l.usedVCPU, api.VCPUSlots)
	}
	for _, app := range propApps { // §6.2-1
		if got := l.perApp[app.id]; got > app.conc {
			t.Fatalf("step %d: app %q concurrency=%d exceeded cap %d", step, app.id, got, app.conc)
		}
	}
}
