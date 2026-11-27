//go:build metal

// adr: 20261114010000 — require real KVM-only receipts for every native Run wake.
package runlifecycle_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/jailsetup"
)

func (f *runFixture) requireNativeKVMOnlyDevice(t *testing.T, instance string) {
	t.Helper()
	if os.Getenv("FAAS_RUN_LIFECYCLE_NATIVE_RECOVERY") != "1" {
		return
	}
	if uuid.Validate(instance) != nil {
		t.Fatal("invalid native Run UUID")
	}
	root := filepath.Join(fcvm.JailChrootBase, ".native-processes")
	data, err := os.ReadFile(filepath.Join(root, instance+".json")) //nolint:forbidigo // Canonical fixture UUID in this namespace's private journal.
	if err != nil {
		t.Fatal(err)
	}
	var owner struct {
		Generation string `json:"generation"`
		PID        int    `json:"pid"`
		StartTime  uint64 `json:"start_time"`
	}
	if json.Unmarshal(data, &owner) != nil || uuid.Validate(owner.Generation) != nil {
		t.Fatal("invalid native wake owner")
	}
	directory := filepath.Join(root, "helpers", owner.Generation)
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(directory, entry.Name())) //nolint:forbidigo // Root-owned native helper records for this exact fixture generation.
		if err != nil {
			t.Fatal(err)
		}
		var record struct {
			Device *struct {
				Scope        jailsetup.DeviceSetupScope    `json:"scope"`
				InputsClosed bool                          `json:"inputs_closed"`
				Receipt      *jailsetup.DeviceSetupReceipt `json:"receipt"`
			} `json:"jail_device"`
		}
		if json.Unmarshal(data, &record) != nil {
			t.Fatal("invalid native device record")
		}
		if record.Device == nil {
			continue
		}
		d := record.Device
		if d.Scope.Version != 2 || d.Scope.Generation != owner.Generation || d.Scope.PID != owner.PID || d.Scope.StartTime != owner.StartTime || !d.InputsClosed || d.Receipt == nil || d.Receipt.Validate(d.Scope) != nil {
			t.Fatal("native wake lacks its original KVM-only receipt")
		}
		found++
	}
	if found != 1 {
		t.Fatalf("native wake has %d KVM-only receipts, want one", found)
	}
	t.Logf("native device witness: run=%s scope_version=2 kvm_api=12 tun_accessible=false", instance)
}
