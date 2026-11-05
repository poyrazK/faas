// adr: 20261024105607 — FAAS_RESTORE_RESEED_THREAD boots app VMs with the
// thread-mode reseed barrier client; builders and networkless execution
// guests keep their command line.
package fcvm

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestWithRestoreReseedModeAddsBootArgToAppVMs(t *testing.T) {
	cfg := BuildColdBootConfig(ColdBootSpec{KernelKey: "k", BaseKey: "b", LayerKey: "l", VcpuCount: 2, MemSizeMiB: 256, Tap: "tap0"}, 1)
	if strings.Contains(cfg.BootSource.BootArgs, api.RestoreReseedThreadBootArg) {
		t.Fatal("BuildColdBootConfig must leave the reseed mode to the VMM")
	}
	app := Lease{Instance: "app"}
	if got := (&JailerVMM{}).withRestoreReseedMode(app, cfg); got.BootSource.BootArgs != cfg.BootSource.BootArgs {
		t.Fatalf("boot args changed while the switch is off: %q", got.BootSource.BootArgs)
	}
	on := (&JailerVMM{}).WithRestoreReseedThread(true)
	got := on.withRestoreReseedMode(app, cfg)
	if !strings.HasSuffix(got.BootSource.BootArgs, " "+api.RestoreReseedThreadBootArg) {
		t.Fatalf("boot args = %q, want %q appended", got.BootSource.BootArgs, api.RestoreReseedThreadBootArg)
	}
	if again := on.withRestoreReseedMode(app, got); again.BootSource.BootArgs != got.BootSource.BootArgs {
		t.Fatalf("boot arg added twice: %q", again.BootSource.BootArgs)
	}
	for name, l := range map[string]Lease{
		"builder":     {Instance: "b", IsBuilder: true},
		"networkless": {Instance: "n", Networkless: true},
	} {
		if kept := on.withRestoreReseedMode(l, cfg); kept.BootSource.BootArgs != cfg.BootSource.BootArgs {
			t.Errorf("%s boot args = %q, want unchanged", name, kept.BootSource.BootArgs)
		}
	}
}
