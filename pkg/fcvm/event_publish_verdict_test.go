// adr: 20261031102000 — app VMs boot knowing this vmmd answers in-guest event
// publishes; builders keep their command line.
package fcvm

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestWithEventPublishVerdict(t *testing.T) {
	cfg := VMConfig{BootSource: BootSource{BootArgs: "console=ttyS0 "}}
	got := withEventPublishVerdict(Lease{}, cfg)
	if want := "console=ttyS0 " + api.EventPublishVerdictBootArg; got.BootSource.BootArgs != want {
		t.Fatalf("boot args = %q, want %q", got.BootSource.BootArgs, want)
	}
	if again := withEventPublishVerdict(Lease{}, got); again.BootSource.BootArgs != got.BootSource.BootArgs {
		t.Fatalf("second pass added the flag again: %q", again.BootSource.BootArgs)
	}
	if builder := withEventPublishVerdict(Lease{IsBuilder: true}, cfg); builder.BootSource.BootArgs != cfg.BootSource.BootArgs {
		t.Fatalf("builder boot args = %q, want unchanged", builder.BootSource.BootArgs)
	}
}
