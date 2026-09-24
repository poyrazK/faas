// adr: 192
// spec: §6.3
package fcvm

import (
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func v2CaptureKey(id string) string {
	return state.SnapshotCaptureMemKey("dep-1", state.SnapshotTierInit, id)
}

// A restore of a capture whose drive already holds identical pre-boot files
// skips the loop mount; any other restore writes exactly as before.
func TestStagePreBootFilesUnless_SkipsOnlyKnownIdenticalCaptures(t *testing.T) {
	fakeLoopMounts(t)
	v := newStagingVMM(t, "cold")
	secrets, env, resolver := []byte(`{"S":"x"}`), []byte(`{"K":"v"}`), "10.100.0.1"

	// Cold boot writes and records what the instance's drive holds.
	if err := v.stagePreBootFiles("cold", nil, secrets, env, resolver, false); err != nil {
		t.Fatal(err)
	}
	capture := v2CaptureKey("c1")
	v.preBoot.captured("cold", capture)

	for _, tc := range []struct {
		name       string
		captureKey string
		secrets    []byte
		env        []byte
		resolver   string
		wantSkip   bool
	}{
		{name: "same capture, same inputs", captureKey: capture, secrets: secrets, env: env, resolver: resolver, wantSkip: true},
		{name: "rotated secret", captureKey: capture, secrets: []byte(`{"S":"y"}`), env: env, resolver: resolver},
		{name: "changed env", captureKey: capture, secrets: secrets, env: []byte(`{"K":"w"}`), resolver: resolver},
		{name: "different resolver", captureKey: capture, secrets: secrets, env: env, resolver: "10.100.0.2"},
		{name: "capture this vmmd never saw", captureKey: v2CaptureKey("other"), secrets: secrets, env: env, resolver: resolver},
		{name: "cold boot path", captureKey: "", secrets: secrets, env: env, resolver: resolver},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sessions, _ := fakeLoopMounts(t) // a fresh drive per restore
			inst := "restore-" + tc.name
			v2 := newStagingVMMFrom(t, v, inst)
			before := *sessions
			skipped, err := v2.stagePreBootFilesUnless(inst, tc.captureKey, nil, tc.secrets, tc.env, tc.resolver, false)
			if err != nil {
				t.Fatal(err)
			}
			if skipped != tc.wantSkip {
				t.Fatalf("skipped = %v, want %v", skipped, tc.wantSkip)
			}
			if mounted := *sessions - before; mounted != map[bool]int{true: 0, false: 1}[tc.wantSkip] {
				t.Fatalf("loop mount sessions = %d (skipped=%v)", mounted, skipped)
			}
		})
	}
}

// newStagingVMMFrom gives instance a chroot with drive1 while sharing v's
// ledger, the way one vmmd restores many instances.
func newStagingVMMFrom(t *testing.T, v *JailerVMM, instance string) *JailerVMM {
	t.Helper()
	fresh := newStagingVMM(t, instance)
	fresh.preBoot = v.preBoot
	return fresh
}

// A skipped restore passes its digest on, so a capture taken from the
// restored instance is itself skippable on the next restore.
func TestPreBootLedger_ChainsThroughSkippedRestores(t *testing.T) {
	sessions, _ := fakeLoopMounts(t)
	v := newStagingVMM(t, "a")
	if err := v.stagePreBootFiles("a", nil, nil, nil, "10.100.0.1", false); err != nil {
		t.Fatal(err)
	}
	v.preBoot.captured("a", v2CaptureKey("c1"))
	b := newStagingVMMFrom(t, v, "b")
	if skipped, err := b.stagePreBootFilesUnless("b", v2CaptureKey("c1"), nil, nil, nil, "10.100.0.1", false); err != nil || !skipped {
		t.Fatalf("first restore skipped=%v err=%v", skipped, err)
	}
	b.preBoot.captured("b", v2CaptureKey("c2"))
	c := newStagingVMMFrom(t, v, "c")
	before := *sessions
	if skipped, err := c.stagePreBootFilesUnless("c", v2CaptureKey("c2"), nil, nil, nil, "10.100.0.1", false); err != nil || !skipped {
		t.Fatalf("restore of a capture from a skipped restore: skipped=%v err=%v", skipped, err)
	}
	if *sessions != before {
		t.Fatal("chained restore still mounted drive1")
	}
}

// Legacy captures carry no drive, so a restore stages the pristine app layer
// and must always write; an unknown instance records nothing.
func TestPreBootLedger_OnlyV2CapturesOfKnownInstances(t *testing.T) {
	p := newPreBootLedger()
	p.instanceHas("i", "digest")
	p.captured("i", "snap/dep-1/mem")
	if p.captureHas("snap/dep-1/mem", "digest") {
		t.Fatal("legacy capture key recorded")
	}
	p.captured("unknown", v2CaptureKey("c1"))
	if p.captureHas(v2CaptureKey("c1"), "digest") {
		t.Fatal("capture of an instance with no recorded writes recorded")
	}
	p.captured("i", v2CaptureKey("c1"))
	if !p.captureHas(v2CaptureKey("c1"), "digest") {
		t.Fatal("v2 capture of a known instance not recorded")
	}
	p.forget("i")
	p.captured("i", v2CaptureKey("c2"))
	if p.captureHas(v2CaptureKey("c2"), "digest") {
		t.Fatal("forgotten instance still recorded")
	}
	var nilLedger *preBootLedger
	nilLedger.captured("i", v2CaptureKey("c3"))
	if nilLedger.captureHas(v2CaptureKey("c3"), "digest") {
		t.Fatal("nil ledger reported a capture")
	}
}

// The capture map is bounded and evicts the oldest entry first.
func TestPreBootLedger_Bounded(t *testing.T) {
	p := newPreBootLedger()
	p.instanceHas("i", "d")
	for n := 0; n < preBootLedgerMaxCaptures+3; n++ {
		p.captured("i", v2CaptureKey(fmt.Sprintf("c%d", n)))
	}
	if len(p.captures) != preBootLedgerMaxCaptures {
		t.Fatalf("captures = %d, want %d", len(p.captures), preBootLedgerMaxCaptures)
	}
	if p.captureHas(v2CaptureKey("c0"), "d") {
		t.Fatal("oldest capture not evicted")
	}
}

// Every input feeds the digest, so no changed payload can be skipped.
func TestPreBootFileWriters_DigestCoversEveryInput(t *testing.T) {
	wl := func(env string) []WorkloadSpec {
		return []WorkloadSpec{
			{Name: "main", Type: "app", RamMB: 256, Port: 8080, Essential: true},
			{Name: "metrics", Type: "sidecar", RamMB: 64, Port: 9100, preparedEnvJSON: []byte(env)},
		}
	}
	digest := func(w []WorkloadSpec, s, e []byte, ip string, task bool) string {
		t.Helper()
		_, d, err := preBootFileWriters(w, s, e, ip, task)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	base := digest(wl(`{"A":"1"}`), []byte("s"), []byte("e"), "10.100.0.1", false)
	if again := digest(wl(`{"A":"1"}`), []byte("s"), []byte("e"), "10.100.0.1", false); again != base {
		t.Fatal("digest is not deterministic")
	}
	for name, d := range map[string]string{
		"secrets":             digest(wl(`{"A":"1"}`), []byte("t"), []byte("e"), "10.100.0.1", false),
		"env":                 digest(wl(`{"A":"1"}`), []byte("s"), []byte("f"), "10.100.0.1", false),
		"resolver":            digest(wl(`{"A":"1"}`), []byte("s"), []byte("e"), "10.100.0.2", false),
		"app task":            digest(wl(`{"A":"1"}`), []byte("s"), []byte("e"), "10.100.0.1", true),
		"sidecar env":         digest(wl(`{"A":"2"}`), []byte("s"), []byte("e"), "10.100.0.1", false),
		"secret/env boundary": digest(wl(`{"A":"1"}`), []byte("se"), []byte(""), "10.100.0.1", false),
	} {
		if d == base {
			t.Errorf("changing %s did not change the digest", name)
		}
	}
}
