// adr: 381 — VM inventory includes processes independent of resource samples.
package fcvm

import (
	"os/exec"
	"testing"
	"time"
)

func TestInstanceInventoryUsesProcessOwnerIncludingEmptyAndBooting(t *testing.T) {
	vmm := NewJailerVMM("/tmp/unused", time.Second)
	manager := NewManager(&fakeRunner{}, vmm, Paths{Kernel: "/k"}, "test", nil, nil)
	ids, complete := manager.InstanceInventory()
	if !complete || len(ids) != 0 {
		t.Fatalf("empty inventory=%v/%v", ids, complete)
	}
	vmm.mu.Lock()
	vmm.proc["booting-vm"] = &exec.Cmd{}
	vmm.mu.Unlock()
	ids, complete = manager.InstanceInventory()
	if !complete || len(ids) != 1 || ids[0] != "booting-vm" {
		t.Fatalf("booting inventory=%v/%v", ids, complete)
	}
	// A retained Manager entry is cleanup state, not proof of a live process.
	manager.RegisterInstanceForTest("exited-vm", "deployment")
	if len(manager.LiveInstances()) == 0 {
		t.Fatal("fixture lacks retained Manager entry")
	}
	ids, _ = manager.InstanceInventory()
	if len(ids) != 1 || ids[0] != "booting-vm" {
		t.Fatalf("retained cleanup entry reported live: %v", ids)
	}
}

func TestInstanceInventoryUnknownVMMCannotAssertEmpty(t *testing.T) {
	manager := NewManager(&fakeRunner{}, &fakeVMM{}, Paths{Kernel: "/k"}, "test", nil, nil)
	if _, complete := manager.InstanceInventory(); complete {
		t.Fatal("unknown VMM asserted complete empty inventory")
	}
}
