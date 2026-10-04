//go:build linux || darwin

// adr: 532 — qualification admission and retirement retain the original native owner.
package fcvm

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

func nativeQualificationManagerFixture(t *testing.T) (*Manager, *JailerVMM, state.EnvironmentQualificationExecution, WakeRequest, context.Context) {
	t.Helper()
	m, v, _ := nativeManagerFixture(t)
	_, frame, ctx := nativeQualificationFixture(t)
	m.WithNativeQualificationNodeID(frame.NodeID)
	ctx = wire.WithContext(ctx, wire.CorrelationFields{WakeID: frame.WakeID})
	req := WakeRequest{Instance: frame.InstanceID, AppID: frame.AppID, DeploymentID: frame.DeploymentID,
		AccountID: uuid.NewString(), Plan: api.PlanHobby, MemSizeMiB: frame.RAMMB, VcpuCount: 2, CPUMillicores: 1000,
		BaseKey: "base/runtime.ext4", LayerKey: frame.Artifact.RootfsKey}
	return m, v, frame, req, ctx
}

func TestNativeQualificationManagerRejectsChangedBootBeforeAuthority(t *testing.T) {
	for _, change := range []string{"unconfigured", "node", "instance", "app", "deployment", "memory", "artifact", "path_only", "account", "plan", "snapshot", "paused", "execution", "app_task", "builder", "wake"} {
		t.Run(change, func(t *testing.T) {
			m, v, frame, req, ctx := nativeQualificationManagerFixture(t)
			switch change {
			case "unconfigured":
				m.WithNativeQualificationNodeID("")
			case "node":
				frame.NodeID = uuid.NewString()
			case "instance":
				req.Instance = uuid.NewString()
			case "app":
				req.AppID = uuid.NewString()
			case "deployment":
				req.DeploymentID = uuid.NewString()
			case "memory":
				req.MemSizeMiB++
			case "artifact":
				req.LayerKey = "apps/replacement.ext4"
			case "path_only":
				frame.Artifact.RootfsPath, frame.Artifact.RootfsKey = "/legacy/qualified.ext4", ""
			case "account":
				req.AccountID = ""
			case "plan":
				req.Plan = "unknown"
			case "snapshot":
				req.Snapshot = &Snapshot{}
			case "paused":
				req.KeepPaused = true
			case "execution":
				req.ExecutionOnly = true
			case "app_task":
				req.AppTaskOnly = true
			case "builder":
				req.ExportDir = t.TempDir()
			case "wake":
				ctx = wire.WithContext(ctx, wire.CorrelationFields{WakeID: uuid.NewString()})
			}
			if _, err := m.WakeEnvironmentQualification(ctx, frame, req); err == nil {
				t.Fatal("changed request gained boot authority")
			}
			j := v.nativeRecovery.journal.qualifications(frame.NodeID)
			path, err := j.path(frame.InstanceID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("rejected boot published incoming authority", err)
			}
			if m.alloc.InUse() != 0 || m.LiveCount() != 0 || len(m.run.(*fakeRunner).commands) != 0 {
				t.Fatal("rejected boot reached allocation or native effects")
			}
		})
	}
}

func TestNativeQualificationManagerRetirementRequiresCompleteOriginalProof(t *testing.T) {
	m, v, frame, req, ctx := nativeQualificationManagerFixture(t)
	j := v.nativeRecovery.journal.qualifications(frame.NodeID)
	m.run = &nativeOwnershipRunner{journal: j.owner, instance: frame.InstanceID}
	cause := errors.New("original resources are still present")
	v.nativeRecovery.resources = func(Lease, netns.Config) error { return cause }
	if _, err := m.WakeEnvironmentQualification(ctx, frame, req); !errors.Is(err, cause) {
		t.Fatalf("failed cleanup was lost: %v", err)
	}
	bound, err := j.read(frame.InstanceID)
	if err != nil || bound.NativeGeneration == "" || bound.NativeLease.Instance != frame.InstanceID || m.alloc.InUse() != 1 {
		t.Fatal("original native reservation was not retained", err)
	}
	if _, err := m.WakeEnvironmentQualification(ctx, frame, req); err == nil {
		t.Fatal("duplicate delivery created a replacement")
	}
	proof, err := m.RetireEnvironmentQualification(ctx, frame)
	if !errors.Is(err, cause) || proof != (state.EnvironmentQualificationRetirement{}) || m.alloc.InUse() != 1 {
		t.Fatalf("incomplete removal supplied retirement or released the lease: %+v %v", proof, err)
	}
	v.nativeRecovery.resources = func(Lease, netns.Config) error { return nil }
	proof, err = m.RetireEnvironmentQualification(ctx, frame)
	if err != nil || proof.Kind != state.QualificationNativeRetired || proof.ReceiptID != bound.Generation ||
		proof.NativeGeneration != bound.NativeGeneration || proof.KernelBootID != bound.KernelBootID || !proof.ProcessesExited || !proof.ResourcesRemoved ||
		m.alloc.InUse() != 0 || m.LiveCount() != 0 || len(m.cidToID) != 0 {
		t.Fatalf("original retirement was not acknowledged: %+v %v", proof, err)
	}
	again, err := m.RetireEnvironmentQualification(ctx, frame)
	if err != nil || again != proof {
		t.Fatalf("retry minted a different native receipt: %+v %v", again, err)
	}
	if _, err := m.WakeEnvironmentQualification(ctx, frame, req); err == nil {
		t.Fatal("late delivery recreated a retired attempt")
	}
}

func TestNativeQualificationManagerRetireBeforeCreateExcludesDelayedDeliveryWithoutReceipt(t *testing.T) {
	m, v, frame, req, ctx := nativeQualificationManagerFixture(t)
	proof, err := m.RetireEnvironmentQualification(ctx, frame)
	if !errors.Is(err, state.ErrConflict) || proof != (state.EnvironmentQualificationRetirement{}) {
		t.Fatal("incoming tombstone was substituted for physical proof", err)
	}
	record, err := v.nativeRecovery.journal.qualifications(frame.NodeID).read(frame.InstanceID)
	if err != nil || !record.Revoked || record.CreateStarted || record.NativeGeneration != "" {
		t.Fatal("original revocation was not durable", err)
	}
	if _, err := m.WakeEnvironmentQualification(ctx, frame, req); err == nil {
		t.Fatal("delayed delivery passed the original revocation")
	}
	if m.alloc.InUse() != 0 || len(m.run.(*fakeRunner).commands) != 0 {
		t.Fatal("delayed delivery reached native effects")
	}
}

func TestNativeQualificationManagerCannotRetireForeignPhysicalOwner(t *testing.T) {
	m, v, frame, _, ctx := nativeQualificationManagerFixture(t)
	lease := qualificationLease(frame.InstanceID)
	if err := v.prepareNativeLease(ctx, lease); err != nil {
		t.Fatal(err)
	}
	before, err := v.nativeRecovery.journal.read(frame.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := m.RetireEnvironmentQualification(ctx, frame)
	if !errors.Is(err, state.ErrConflict) || proof != (state.EnvironmentQualificationRetirement{}) {
		t.Fatal("unbound incoming request borrowed foreign native authority", err)
	}
	after, err := v.nativeRecovery.journal.read(frame.InstanceID)
	if err != nil || after != before || len(m.run.(*fakeRunner).commands) != 0 {
		t.Fatal("retirement touched a foreign physical owner", err)
	}
}

func TestNativeQualificationManagerMissingPhysicalPublicationPreservesReservation(t *testing.T) {
	m, v, frame, req, ctx := nativeQualificationManagerFixture(t)
	cause := errors.New("physical publication unavailable")
	v.nativeRecovery.journal.writeRecord = func(string, nativeLaunchRecord) error { return cause }
	if _, err := m.WakeEnvironmentQualification(ctx, frame, req); !errors.Is(err, cause) {
		t.Fatal("physical publication uncertainty was ignored", err)
	}
	v.nativeRecovery.journal.writeRecord = nil
	proof, err := m.RetireEnvironmentQualification(ctx, frame)
	if err == nil || proof != (state.EnvironmentQualificationRetirement{}) || m.alloc.InUse() != 1 || m.pendingCleanup[frame.InstanceID] == nil {
		t.Fatal("missing physical parent released uncertain reservation", err)
	}
}

func TestNativeQualificationRetirementCannotBorrowChangedFrameOrPhysicalIdentity(t *testing.T) {
	for _, change := range []string{"frame", "generation", "lease", "unremoved"} {
		t.Run(change, func(t *testing.T) {
			m, v, frame, req, ctx := nativeQualificationManagerFixture(t)
			j := v.nativeRecovery.journal.qualifications(frame.NodeID)
			m.run = &nativeOwnershipRunner{journal: j.owner, instance: frame.InstanceID}
			if _, err := m.WakeEnvironmentQualification(ctx, frame, req); err == nil {
				t.Fatal("fixture should fail network setup")
			}
			if _, err := j.revoke(ctx, frame); err != nil {
				t.Fatal(err)
			}
			physical, err := j.owner.read(frame.InstanceID)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "frame":
				frame.CleanupToken = uuid.NewString()
			case "generation":
				physical.Generation = uuid.NewString()
			case "lease":
				physical.Lease.CPUMillicores++
			case "unremoved":
				physical.ResourcesRemoved = false
			}
			if err := j.owner.write(physical); err != nil {
				t.Fatal(err)
			}
			if proof, err := j.retirement(ctx, frame); err == nil || proof != (state.EnvironmentQualificationRetirement{}) {
				t.Fatal("changed identity supplied the original receipt", err)
			}
		})
	}
}
