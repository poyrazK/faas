// adr: 590
package fcvm

import (
	"testing"
	"time"
)

func TestRuntimeSourcesRemainPinnedWhenProcessRetirementFails(t *testing.T) {
	f := newRuntimeDriveFixture(t)
	f.pin(t)
	handoff := f.measure(t)
	file := handoff.drives[0].file
	f.vmm.recs = map[string]*instanceRecord{f.lease.Instance: {done: make(chan struct{})}}
	f.vmm.destroyWait = time.Millisecond
	if err := f.vmm.Kill(t.Context(), f.lease); err == nil {
		t.Fatal("unconfirmed process retirement succeeded")
	}
	if _, err := file.Stat(); err != nil || handoff.closed || f.vmm.runtimeSources().root == "" {
		t.Fatalf("failed retirement released source ownership: %v", err)
	}
}
