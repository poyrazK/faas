//go:build linux

// adr: 2026101710 amendment 1 — an earlier vmmd's ordinary VMs are cleaned up
// only after its owner record proves that incarnation exited.
package fcvm

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func nativeOwnedLaunch(t *testing.T, owners bool) (*JailerVMM, nativeLaunchRecord) {
	t.Helper()
	_, v, _ := nativeManagerFixture(t)
	r := v.nativeRecovery
	r.journal.owners = owners
	if err := r.acquireDaemonOwnership(t.Context()); err != nil {
		t.Fatal(err)
	}
	lease := leaseForSlot("owned-launch", 1)
	lease.Plan = api.PlanHobby
	if err := r.journal.prepare(t.Context(), lease); err != nil {
		t.Fatal(err)
	}
	record, err := r.journal.read(lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	r.remember(record)
	return v, record
}

func TestNativeLaunchOwnerNamesThisDaemon(t *testing.T) {
	_, record := nativeOwnedLaunch(t, true)
	if record.Version != 3 || record.Owner.Protocol != nativeLaunchOwnerProtocol || record.Owner.PID != os.Getpid() || record.Owner.StartTime == 0 || record.Producer != (nativeProducerIdentity{}) {
		t.Fatalf("launch record does not name its owner: %+v", record)
	}
	_, legacy := nativeOwnedLaunch(t, false)
	if legacy.Version != 1 || legacy.Owner != (nativeProducerIdentity{}) {
		t.Fatalf("owner records were written without the setting: %+v", legacy)
	}
}

func TestNativeLaunchOwnerProvesEarlierGenerationExit(t *testing.T) {
	v, record := nativeOwnedLaunch(t, true)
	r := v.nativeRecovery
	// This vmmd owns the generation: its own identity, no exit proof.
	r.producerExited = func(context.Context, nativeProducerIdentity) error {
		t.Fatal("the local generation probed its own daemon")
		return nil
	}
	if err := v.requireNativeProducerRetirement(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	// A replacement vmmd does not own it. A live original refuses cleanup.
	r.mu.Lock()
	delete(r.owned, record.Lease.Instance)
	r.mu.Unlock()
	alive := errors.New("original daemon is still running")
	var probed nativeProducerIdentity
	r.producerExited = func(_ context.Context, p nativeProducerIdentity) error { probed = p; return alive }
	if err := v.requireNativeProducerRetirement(t.Context(), record); !errors.Is(err, alive) || probed != record.Owner {
		t.Fatalf("live owner: err=%v probed=%+v", err, probed)
	}
	r.producerExited = func(context.Context, nativeProducerIdentity) error { return nil }
	if err := v.requireNativeProducerRetirement(t.Context(), record); err != nil {
		t.Fatalf("exited owner kept its VMs quarantined: %v", err)
	}
	// Without the ownership lock there is no proof at all.
	closed := r.daemonLock
	r.mu.Lock()
	r.daemonLock = nil
	r.mu.Unlock()
	if err := v.requireNativeProducerRetirement(t.Context(), record); err == nil {
		t.Fatal("cleanup proceeded without daemon ownership")
	}
	r.mu.Lock()
	r.daemonLock = closed
	r.mu.Unlock()
	// A record that names no owner keeps the earlier guard.
	legacy := record
	legacy.Version, legacy.Owner = 1, nativeProducerIdentity{}
	if err := v.requireNativeProducerRetirement(t.Context(), legacy); err == nil || !strings.Contains(err.Error(), "not yet attested") {
		t.Fatalf("an unnamed earlier generation was cleaned up: %v", err)
	}
}

func TestNativeLaunchOwnerChangedLocalIdentityRefused(t *testing.T) {
	v, record := nativeOwnedLaunch(t, true)
	record.Owner.StartTime++
	if err := v.requireNativeProducerRetirement(t.Context(), record); err == nil {
		t.Fatal("a local generation accepted a different daemon incarnation")
	}
}
