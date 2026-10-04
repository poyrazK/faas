//go:build linux || darwin

// adr: 568 — incoming authority must bind the original physical producer.
package fcvm

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func qualificationLease(instance string) Lease {
	lease := leaseForSlot(instance, 3)
	lease.Plan, lease.MemoryMaxMiB, lease.CPUMillicores = api.PlanHobby, 256, 1000
	return lease
}

func TestNativeQualificationBindingPrecedesPhysicalPublication(t *testing.T) {
	j, frame, ctx := nativeQualificationFixture(t)
	claimed, err := j.claim(ctx, frame)
	if err != nil {
		t.Fatal(err)
	}
	lease := qualificationLease(frame.InstanceID)
	lease.processGeneration = 42
	j.owner.writeRecord = func(path string, physical nativeLaunchRecord) error {
		incoming, err := j.read(frame.InstanceID)
		if err != nil || incoming.NativeGeneration != physical.Generation || !sameNativePhysicalLease(incoming.NativeLease, lease) ||
			incoming.NativeLease.processGeneration != 0 || incoming.Execution != frame || incoming.Generation != claimed.Generation {
			t.Fatalf("physical publication preceded durable original binding: %s %v", incoming, err)
		}
		return writeNativeLaunchRecord(path, physical)
	}
	producer := nativeQualificationContext(ctx, claimed)
	if err := j.owner.prepare(producer, lease); err != nil {
		t.Fatal(err)
	}
	if err := j.owner.prepare(producer, lease); err == nil {
		t.Fatal("original attempt created a second physical generation")
	}
	physical, err := j.owner.read(frame.InstanceID)
	if err != nil || physical.Generation == claimed.Generation {
		t.Fatal("incoming and physical incarnation identities were conflated", err)
	}
	for _, change := range []string{"cpu", "memory", "startup_boost", "timeout"} {
		changed := lease
		switch change {
		case "cpu":
			changed.CPUMillicores++
		case "memory":
			changed.MemoryMaxMiB++
		case "startup_boost":
			changed.DisableStartupCPUBoost = true
		case "timeout":
			changed.BuildTimeoutSec++
		}
		if ticket, err := j.owner.beginLaunch(producer, changed); err == nil {
			_ = ticket.close()
			t.Fatalf("changed %s borrowed original native launch authority", change)
		}
	}
	ticket, err := j.owner.beginLaunch(producer, lease)
	if err != nil {
		t.Fatal(err)
	}
	if err := ticket.close(); err != nil {
		t.Fatal(err)
	}
	if _, err := j.owner.replace(producer, lease, physical.Generation, false); err == nil {
		t.Fatal("restore fallback replaced a qualification's original generation")
	}
}

func TestNativeQualificationPhysicalPublicationFailureKeepsRestartReservation(t *testing.T) {
	m, v, _ := nativeManagerFixture(t)
	_, frame, ctx := nativeQualificationFixture(t)
	j := v.nativeRecovery.journal.qualifications(frame.NodeID)
	claimed, err := j.claim(ctx, frame)
	if err != nil {
		t.Fatal(err)
	}
	lease := qualificationLease(frame.InstanceID)
	cause := errors.New("physical publication unavailable")
	j.owner.writeRecord = func(string, nativeLaunchRecord) error { return cause }
	producer := nativeQualificationContext(ctx, claimed)
	if err := j.owner.prepare(producer, lease); !errors.Is(err, cause) {
		t.Fatalf("physical publication uncertainty ignored: %v", err)
	}
	if _, err := j.owner.read(frame.InstanceID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected physical record: %v", err)
	}
	if err := m.RecoverNativeProcesses(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !m.alloc.hasRecoveredInstance(frame.InstanceID) || len(m.nativeRecovered[frame.InstanceID]) != 1 || m.LiveCount() != 0 || len(m.cidToID) != 0 {
		t.Fatal("uncertain original lease was released or promoted to runtime authority")
	}
	if _, err := j.revoke(t.Context(), frame); err != nil {
		t.Fatal(err)
	}
	j.owner.writeRecord = nil
	if err := j.owner.prepare(producer, lease); err == nil {
		t.Fatal("delayed preparation bypassed the recovered revocation")
	}
	if !m.alloc.hasRecoveredInstance(frame.InstanceID) {
		t.Fatal("incoming revocation was treated as a retirement receipt")
	}
}

func TestNativeQualificationGenericAdmissionAndAlternateSpellingsCannotBorrowOwner(t *testing.T) {
	m, v, _ := nativeManagerFixture(t)
	_, frame, ctx := nativeQualificationFixture(t)
	j := v.nativeRecovery.journal.qualifications(frame.NodeID)
	if _, err := j.claim(ctx, frame); err != nil {
		t.Fatal(err)
	}
	for _, instance := range []string{frame.InstanceID, strings.ReplaceAll(frame.InstanceID, "-", ""), strings.ToUpper(frame.InstanceID)} {
		if err := j.owner.prepare(ctx, qualificationLease(instance)); err == nil {
			t.Fatal("generic preparation borrowed a reserved UUID", instance)
		}
		if _, err := m.Wake(ctx, WakeRequest{Instance: instance, Plan: api.PlanHobby, MemSizeMiB: 256}); err == nil {
			t.Fatal("generic wake borrowed incoming authority", instance)
		}
		if _, _, _, err := m.beginLiveInstanceFlight(ctx, instance); err == nil {
			t.Fatal("generic resumable operation borrowed incoming authority", instance)
		}
	}
	if m.alloc.InUse() != 0 || len(m.run.(*fakeRunner).commands) != 0 || m.LiveCount() != 0 {
		t.Fatal("rejected generic admission reached allocation or native effects")
	}
}

func TestNativeQualificationClaimCannotAdoptAnExistingPhysicalUUID(t *testing.T) {
	for _, spelling := range []string{"original", "compact", "uppercase"} {
		t.Run(spelling, func(t *testing.T) {
			j, frame, ctx := nativeQualificationFixture(t)
			instance := frame.InstanceID
			if spelling == "compact" {
				instance = strings.ReplaceAll(instance, "-", "")
			} else if spelling == "uppercase" {
				instance = strings.ToUpper(instance)
			}
			if err := j.owner.prepare(ctx, qualificationLease(instance)); err != nil {
				t.Fatal(err)
			}
			if _, err := j.claim(ctx, frame); err == nil {
				t.Fatal("qualification borrowed an existing physical owner")
			}
		})
	}
}

func TestNativeQualificationRevocationJoinsLaunchAuthorizationAndRecoversLostWrite(t *testing.T) {
	j, frame, ctx := nativeQualificationFixture(t)
	claimed, err := j.claim(ctx, frame)
	if err != nil {
		t.Fatal(err)
	}
	producer := nativeQualificationContext(ctx, claimed)
	lease := qualificationLease(frame.InstanceID)
	if err := j.owner.prepare(producer, lease); err != nil {
		t.Fatal(err)
	}
	ticket, err := j.owner.beginLaunch(producer, lease)
	if err != nil {
		t.Fatal(err)
	}
	cleanup, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	_, err = j.revoke(cleanup, frame)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("revocation bypassed the launch authorization window: %v", err)
	}
	if err := ticket.close(); err != nil {
		t.Fatal(err)
	}
	cause := errors.New("physical revoke publication failed")
	j.owner.writeRecord = func(string, nativeLaunchRecord) error { return cause }
	if _, err := j.revoke(t.Context(), frame); !errors.Is(err, cause) {
		t.Fatalf("physical fence failure ignored: %v", err)
	}
	incoming, err := j.read(frame.InstanceID)
	if err != nil || !incoming.Revoked {
		t.Fatal("incoming fence was not durable before physical revocation", err)
	}
	if ticket, err := j.owner.beginLaunch(producer, lease); err == nil {
		_ = ticket.close()
		t.Fatal("old producer bypassed the incoming fence")
	}
	j.owner.writeRecord = nil
	restarted := j.owner.qualifications(frame.NodeID)
	if _, err := restarted.revoke(t.Context(), frame); err != nil {
		t.Fatal("physical revocation was not recoverable", err)
	}
	physical, err := j.owner.read(frame.InstanceID)
	if err != nil || !physical.Revoked || physical.ExitConfirmed || physical.ResourcesRemoved {
		t.Fatal("revocation fabricated retirement evidence", err)
	}
	if _, err := restarted.revoke(t.Context(), frame); err != nil {
		t.Fatal("exact revocation was not idempotent", err)
	}
}

func TestNativeQualificationRecoveryRejectsChangedPhysicalGenerationAndLease(t *testing.T) {
	for _, change := range []string{"generation", "cpu", "boot", "unbound"} {
		t.Run(change, func(t *testing.T) {
			j, frame, ctx := nativeQualificationFixture(t)
			claimed, err := j.claim(ctx, frame)
			if err != nil {
				t.Fatal(err)
			}
			lease := qualificationLease(frame.InstanceID)
			if err := j.owner.prepare(nativeQualificationContext(ctx, claimed), lease); err != nil {
				t.Fatal(err)
			}
			parent, err := j.owner.read(frame.InstanceID)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "generation":
				parent.Generation = uuid.NewString()
			case "cpu":
				parent.Lease.CPUMillicores++
			case "boot":
				parent.KernelBootID = uuid.NewString()
			case "unbound":
				incoming, err := j.read(frame.InstanceID)
				if err != nil {
					t.Fatal(err)
				}
				incoming.NativeGeneration, incoming.NativeLease = "", Lease{}
				if err := j.write(incoming); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := j.recoveryLeases(t.Context(), []nativeLaunchRecord{parent}); err == nil {
				t.Fatalf("changed %s gained recovered native ownership", change)
			}
		})
	}
}

func TestNativeQualificationClaimCannotRaceGenericPhysicalPublication(t *testing.T) {
	j, frame, ctx := nativeQualificationFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	j.owner.writeRecord = func(path string, record nativeLaunchRecord) error {
		close(entered)
		<-release
		return writeNativeLaunchRecord(path, record)
	}
	done := make(chan error, 1)
	go func() { done <- j.owner.prepare(ctx, qualificationLease(strings.ToUpper(frame.InstanceID))) }()
	<-entered
	claimCtx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	_, err := j.claim(claimCtx, frame)
	cancel()
	close(release)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("claim interleaved with a generic native producer: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := j.claim(ctx, frame); err == nil {
		t.Fatal("claim adopted the completed generic physical publication")
	}
}

func TestNativeQualificationManagerBindsBeforeNetworkEffects(t *testing.T) {
	m, v, _ := nativeManagerFixture(t)
	_, frame, ctx := nativeQualificationFixture(t)
	j := v.nativeRecovery.journal.qualifications(frame.NodeID)
	claimed, err := j.claim(ctx, frame)
	if err != nil {
		t.Fatal(err)
	}
	runner := &nativeOwnershipRunner{journal: j.owner, instance: frame.InstanceID}
	m.run = runner
	_, err = m.Wake(nativeQualificationContext(ctx, claimed), WakeRequest{Instance: frame.InstanceID, Plan: api.PlanHobby, MemSizeMiB: frame.RAMMB})
	if err == nil || !runner.checked {
		t.Fatal("private native boot did not reach the owned network setup", err)
	}
	bound, err := j.read(frame.InstanceID)
	if err != nil || bound.NativeGeneration == "" || !sameNativePhysicalLease(bound.NativeLease, runner.lease) || bound.Execution != frame {
		t.Fatal("network setup borrowed an unbound native lease", err)
	}
	physical, err := j.owner.read(frame.InstanceID)
	if err != nil || physical.Generation != bound.NativeGeneration || !physical.ResourcesRemoved || !physical.ExitConfirmed {
		t.Fatal("failed private setup lost its original native cleanup identity", err)
	}
	if m.alloc.InUse() != 0 || m.LiveCount() != 0 || len(m.cidToID) != 0 {
		t.Fatal("failed private boot retained runtime authority after confirmed cleanup")
	}
}
