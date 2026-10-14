// adr: 1027
package dbvm

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

func TestSparesAreClaimedOnceAndOnlyByTheirShape(t *testing.T) {
	rt := testRuntime(t, &fakeLeaser{})
	shape := SpareShape{Major: 17, VcpuCount: 1, MemSizeMiB: 512}
	exited := make(chan struct{})
	close(exited)
	rt.vms = map[string]*databaseVM{
		"spare-live": {spare: shape, waiting: true, exited: make(chan struct{})},
		"spare-dead": {spare: shape, waiting: true, exited: exited},
		"database":   {exited: make(chan struct{})},
	}
	if n := rt.waitingSpares(shape); n != 2 {
		t.Fatalf("waiting = %d", n)
	}
	if vm, _ := rt.claimSpare(SpareShape{Major: 16, VcpuCount: 1, MemSizeMiB: 512}); vm != nil {
		t.Fatal("claimed a spare of another major")
	}
	if vm, _ := rt.claimSpare(SpareShape{Major: 17, VcpuCount: 2, MemSizeMiB: 512}); vm != nil {
		t.Fatal("claimed a spare of another size")
	}
	vm, id := rt.claimSpare(shape)
	if vm == nil || id != "spare-live" || vm.waiting {
		t.Fatalf("claim = %v %q", vm, id)
	}
	if vm, _ := rt.claimSpare(shape); vm != nil {
		t.Fatal("a spare was claimed twice, or a dead one claimed")
	}
}

func TestSpareBootArgs(t *testing.T) {
	spec := Spec{KernelPath: "k", BasePath: "b", VolumePath: "v", Major: 17, VcpuCount: 1, MemSizeMiB: 512, GuestCID: 3, VsockUDS: "s", Spare: true, Archive: true}
	cfg, err := BuildConfig(spec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cfg.BootSource.BootArgs, dbguest.SpareArg) || !dbguest.Spare(cfg.BootSource.BootArgs) {
		t.Fatalf("boot args = %q", cfg.BootSource.BootArgs)
	}
	spec.RestoreTarget = spec.RestoreTarget.AddDate(2026, 0, 0)
	if _, err := BuildConfig(spec); err == nil {
		t.Fatal("a spare booting a restore was accepted")
	}
}
