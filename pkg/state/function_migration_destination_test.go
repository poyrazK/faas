package state

// adr: 20261018030615
// These tests simulate native witnesses; SQL effects are real in Pg.

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

type functionDestinationTestStore interface {
	functionMigrationAdmissionTestStore
	FunctionMigrationDestinationStore
	RuntimeConfigMigrationStore
	RuntimeConfigReceiptStore
	InstanceApplicationStandardRuntimeReceiptStore
}

func functionDestinationFixture(t *testing.T, s functionDestinationTestStore) (Instance, runtimeadmission.FunctionMigrationDestinationGrant, runtimeadmission.Receipt, RuntimeConfigMigration) {
	t.Helper()
	ins, req := functionMigrationAdmissionFixture(t, s)
	inputs := RuntimeConfigInputs{Scope: DefaultEnvScope, Boundary: time.Now().UTC().Truncate(time.Microsecond)}
	if err := s.RecordInstanceRuntimeConfigReceipt(t.Context(), ins.ID, ins.WakeID, inputs); err != nil {
		t.Fatal("record source configuration before capture", err)
	}
	g, err := s.IssueFunctionMigrationCapture(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	c := runtimeadmission.SnapshotCapture{Version: runtimeadmission.SnapshotCaptureVersion, Parent: g.Parent.Clone(), CapturedAtUnixNano: time.Now().UnixNano(),
		Memory:       runtimeadmission.CapturedArtifact{StorageKey: g.MemoryKey, Digest: "sha256:" + strings.Repeat("a", 64), Bytes: int64(ins.RAMMB) << 20},
		VMState:      runtimeadmission.CapturedArtifact{StorageKey: g.VMStateKey, Digest: "sha256:" + strings.Repeat("b", 64), Bytes: 4096},
		PrivateDrive: runtimeadmission.CapturedArtifact{StorageKey: g.PrivateDriveKey, Digest: "sha256:" + strings.Repeat("c", 64)}}
	for _, d := range g.Parent.ArtifactConsumption.Drives {
		if d.Source.Role() == "main" {
			c.PrivateDrive.Bytes = d.InjectedBytes
		}
	}
	if err := s.RecordFunctionMigrationCapture(t.Context(), g, c); err != nil {
		t.Fatal("persist capture", err)
	}
	if err := s.CompleteMigrationLease(t.Context(), g.LeaseToken, g.MemoryKey, g.VMStateKey); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkInstanceMigrating(t.Context(), ins.ID, ins.NodeID, g.LeaseToken); err != nil {
		t.Fatal(err)
	}
	node, err := s.UpsertComputeNode(t.Context(), ComputeNode{Name: "function-destination-" + uuid.NewString(), TargetURL: "unix:///tmp/function-destination.sock", VPCPUs: 8, VCPUBudget: api.VCPUSlots, MemMB: 16384, MaxConcurrency: 8, AdmissionCeilingMB: 16384, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	identity := runtimeadmission.Identity{ProtocolVersion: runtimeadmission.ArtifactProtocolVersion, NodeID: node.ID, Incarnation: uuid.NewString()}
	if err := s.RegisterComputeNodeRuntimeIdentity(t.Context(), identity); err != nil {
		t.Fatal(err)
	}
	dreq := FunctionMigrationDestinationRequest{InstanceID: ins.ID, LeaseToken: g.LeaseToken, WakeID: uuid.NewString(), NativeInputHash: strings.Repeat("d", 64), Identity: identity}
	dest, err := s.IssueFunctionMigrationDestination(t.Context(), dreq)
	if err != nil {
		t.Fatal("destination issuance", err)
	}
	again, err := s.IssueFunctionMigrationDestination(t.Context(), dreq)
	if err != nil || !dest.Equal(again) {
		t.Fatal("issuance changed in-flight authority", err)
	}
	drives := g.Parent.ArtifactConsumption.Clone()
	drives.ConfigHash = runtimeadmission.SnapshotLoadCommandHash(false)
	r := runtimeadmission.Receipt{Binding: dest.Binding, NativeInputHash: dreq.NativeInputHash, Netns: "destination-netns", HostIP: "10.199.0.2", LeaseUID: 22001,
		Method: vmmdpb.WakeMethod_WAKE_RESTORE, CompletedAtUnixNano: time.Now().UnixNano(), ArtifactConsumption: drives,
		SnapshotConsumption: runtimeadmission.SnapshotConsumption{Version: runtimeadmission.SnapshotRestoreVersion, CaptureToken: g.CaptureToken, EvidenceHash: dest.Binding.SnapshotEvidenceHash,
			Memory: c.Memory, VMState: c.VMState, PrivateDrive: c.PrivateDrive, MappedMemoryBytes: c.Memory.Bytes}}
	input := RuntimeConfigMigration{ExpectedWakeID: ins.WakeID, WakeID: dest.WakeID, Netns: r.Netns, HostIP: r.HostIP, GuestUID: int(r.LeaseUID), Inputs: &inputs}
	return ins, dest, r, input
}
func functionDestinationLifecycle(t *testing.T, s functionDestinationTestStore) {
	ins, g, r, input := functionDestinationFixture(t, s)
	ctx := t.Context()
	if err := g.Restore.Check(g.Binding, nil, g.Source.MemoryKey, g.Source.VMStateKey, g.Source.FCVersion, g.Restore.Capture.Memory.Bytes, time.Now()); err == nil {
		t.Fatal("ordinary clone permitted same logical ID")
	}
	if err := s.MigrateInstanceOwnerWithRuntimeConfig(ctx, ins.ID, ins.NodeID, g.Binding.NodeID, g.Source.LeaseToken, input); err == nil {
		t.Fatal("published before destination ACK")
	}
	if err := s.MigrateInstanceOwner(ctx, ins.ID, ins.NodeID, g.Binding.NodeID, g.Source.LeaseToken); err == nil {
		t.Fatal("legacy commit bypassed readiness")
	}
	if err := s.ReadyFunctionMigrationDestination(ctx, g, r); err != nil {
		t.Fatal("readiness", err)
	}
	if err := s.ReadyFunctionMigrationDestination(ctx, g, r); err != nil {
		t.Fatal("duplicate readiness", err)
	}
	bad := r.Clone()
	bad.SnapshotConsumption.MappedMemoryBytes--
	if err := s.ReadyFunctionMigrationDestination(ctx, g, bad); err == nil {
		t.Fatal("changed consumed bytes accepted")
	}
	badInput := input
	badInput.WakeID = uuid.NewString()
	if err := s.MigrateInstanceOwnerWithRuntimeConfig(ctx, ins.ID, ins.NodeID, g.Binding.NodeID, g.Source.LeaseToken, badInput); err == nil {
		t.Fatal("wrong destination wake published")
	}
	badScope := *input.Inputs
	badScope.Scope = "preview"
	wrongScope := input
	wrongScope.Inputs = &badScope
	if err := s.MigrateInstanceOwnerWithRuntimeConfig(ctx, ins.ID, ins.NodeID, g.Binding.NodeID, g.Source.LeaseToken, wrongScope); err == nil {
		t.Fatal("destination published another environment's config receipt")
	}
	if err := s.MigrateInstanceOwnerWithRuntimeConfig(ctx, ins.ID, ins.NodeID, g.Binding.NodeID, g.Source.LeaseToken, input); err != nil {
		t.Fatal("ownership commit", err)
	}
	if err := s.CheckFunctionMigrationDestinationCommitted(ctx, g); err != nil {
		t.Fatal("committed receipt lost", err)
	}
	parent, err := s.GetInstanceApplicationStandardRuntimeReceipt(ctx, ins.ID)
	if err != nil || !parent.Equal(r) {
		t.Fatal("subsequent lifecycle did not retain destination receipt", err)
	}
	if _, err := s.IssueFunctionMigrationRecovery(ctx, ins.ID, g.Source.LeaseToken); err == nil {
		t.Fatal("committed destination resumed original source")
	}
	if err := s.RetireFunctionMigrationDestination(ctx, g); err == nil {
		t.Fatal("committed destination rolled back")
	}
	if err := s.DeleteMigrationLease(ctx, g.Source.LeaseToken); err != nil {
		t.Fatal("source cleanup", err)
	}
	if err := s.CheckFunctionMigrationDestinationCommitted(ctx, g); err != nil {
		t.Fatal("source cleanup deleted serving receipt", err)
	}
	if _, ok, err := s.InstanceRuntimeConfigReceipt(ctx, ins.ID); err != nil || !ok {
		t.Fatal("config did not transfer atomically", err)
	}
}
func functionDestinationRollback(t *testing.T, s functionDestinationTestStore) {
	ins, g, r, input := functionDestinationFixture(t, s)
	ctx := t.Context()
	if err := s.ReadyFunctionMigrationDestination(ctx, g, r); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FenceFunctionMigrationSource(ctx, ins.ID, g.Source.LeaseToken); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckFunctionMigrationDestination(ctx, g); err == nil {
		t.Fatal("rollback fence retained restore authority")
	}
	if _, err := s.IssueFunctionMigrationRecovery(ctx, ins.ID, g.Source.LeaseToken); err == nil {
		t.Fatal("source resumed before destination retirement")
	}
	if err := s.DeleteMigrationLease(ctx, g.Source.LeaseToken); err == nil {
		t.Fatal("unconfirmed destination forgotten")
	}
	if err := s.MigrateInstanceOwnerWithRuntimeConfig(ctx, ins.ID, ins.NodeID, g.Binding.NodeID, g.Source.LeaseToken, input); err == nil {
		t.Fatal("late ownership commit survived fence")
	}
	if err := s.RetireFunctionMigrationDestination(ctx, g); err != nil {
		t.Fatal("retire destination", err)
	}
	if _, err := s.IssueFunctionMigrationRecovery(ctx, ins.ID, g.Source.LeaseToken); err != nil {
		t.Fatal("confirmed retirement did not unblock source", err)
	}
}
func functionDestinationCommitRace(t *testing.T, s functionDestinationTestStore) {
	ins, g, r, input := functionDestinationFixture(t, s)
	ctx := t.Context()
	if err := s.ReadyFunctionMigrationDestination(ctx, g, r); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	var commitErr, fenceErr error
	var resolution MigrationCommitResolution
	go func() {
		defer wg.Done()
		commitErr = s.MigrateInstanceOwnerWithRuntimeConfig(ctx, ins.ID, ins.NodeID, g.Binding.NodeID, g.Source.LeaseToken, input)
	}()
	go func() {
		defer wg.Done()
		resolution, fenceErr = s.FenceFunctionMigrationSource(ctx, ins.ID, g.Source.LeaseToken)
	}()
	wg.Wait()
	if fenceErr != nil && !errors.Is(fenceErr, ErrApplicationStandardRuntimeBusy) {
		t.Fatal(fenceErr)
	}
	if commitErr == nil {
		if fenceErr == nil && resolution != MigrationCommitObsolete {
			t.Fatal("two ownership winners", resolution)
		}
		if err := s.CheckFunctionMigrationDestinationCommitted(ctx, g); err != nil {
			t.Fatal(err)
		}
	} else {
		if err := s.CheckFunctionMigrationDestinationCommitted(ctx, g); err == nil {
			t.Fatal("failed commit published destination")
		}
		if _, err := s.IssueFunctionMigrationRecovery(ctx, ins.ID, g.Source.LeaseToken); err == nil {
			t.Fatal("race resumed source before cleanup")
		}
	}
}
func TestFunctionDestinationMem(t *testing.T) {
	t.Run("capacity", func(t *testing.T) { functionDestinationCapacity(t, NewMemStore()) })
	for _, scenario := range []string{"target restart", "source restart", "security policy", "lease expiry"} {
		t.Run(scenario, func(t *testing.T) { functionDestinationDrift(t, NewMemStore(), scenario) })
	}
	for _, test := range []struct {
		name string
		run  func(*testing.T, functionDestinationTestStore)
	}{{"lifecycle", functionDestinationLifecycle}, {"rollback", functionDestinationRollback}, {"commit race", functionDestinationCommitRace}} {
		t.Run(test.name, func(t *testing.T) { test.run(t, NewMemStore()) })
	}
}

func functionDestinationDrift(t *testing.T, s functionDestinationTestStore, scenario string) {
	ins, g, r, input := functionDestinationFixture(t, s)
	ctx := t.Context()
	if err := s.ReadyFunctionMigrationDestination(ctx, g, r); err != nil {
		t.Fatal(err)
	}
	switch scenario {
	case "target restart":
		if err := s.RegisterComputeNodeRuntimeIdentity(ctx, runtimeadmission.Identity{ProtocolVersion: g.Binding.ProtocolVersion, NodeID: g.Binding.NodeID, Incarnation: uuid.NewString()}); err != nil {
			t.Fatal(err)
		}
	case "source restart":
		b := g.Source.Parent.Binding
		if err := s.RegisterComputeNodeRuntimeIdentity(ctx, runtimeadmission.Identity{ProtocolVersion: b.ProtocolVersion, NodeID: b.NodeID, Incarnation: uuid.NewString()}); err != nil {
			t.Fatal(err)
		}
	case "security policy":
		p := api.AppSecurityPolicyEnforce
		if _, err := s.UpdateApp(ctx, ins.AppID, UpdateAppParams{SetSecurityPolicy: true, SecurityPolicy: &p}); err != nil {
			t.Fatal(err)
		}
	case "lease expiry":
		if err := s.RenewMigrationLease(ctx, g.Source.LeaseToken, ins.NodeID, time.Now().Add(-time.Microsecond)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.CheckFunctionMigrationDestination(ctx, g); err == nil {
		t.Fatal("changed current inputs retained restore permission")
	}
	if err := s.ReadyFunctionMigrationDestination(ctx, g, r); err == nil {
		t.Fatal("stale destination published readiness")
	}
	if err := s.MigrateInstanceOwnerWithRuntimeConfig(ctx, ins.ID, ins.NodeID, g.Binding.NodeID, g.Source.LeaseToken, input); err == nil {
		t.Fatal("stale destination published ownership")
	}
	if _, err := s.FenceFunctionMigrationSource(ctx, ins.ID, g.Source.LeaseToken); err != nil {
		t.Fatal(err)
	}
	if err := s.RetireFunctionMigrationDestination(ctx, g); err != nil {
		t.Fatal("cleanup required fresh resume permission", err)
	}
}

func functionDestinationCapacity(t *testing.T, s functionDestinationTestStore, reopen ...func() functionDestinationTestStore) {
	ins, g, _, _ := functionDestinationFixture(t, s)
	ctx := t.Context()
	node, err := s.ComputeNodeByID(ctx, g.Binding.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	node.AdmissionCeilingMB = ins.RAMMB + api.PerVMOverheadMB
	if _, err := s.UpsertComputeNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	if len(reopen) != 0 {
		s = reopen[0]()
	}
	if _, err := s.CreateInstance(ctx, ins.AppID, ins.DeploymentID, string(StateWaking), ins.RAMMB, node.ID, uuid.NewString()); !errors.Is(err, ErrNodeCapacity) {
		t.Fatal("pending destination lost durable capacity", err)
	}
	if _, err := s.FenceFunctionMigrationSource(ctx, ins.ID, g.Source.LeaseToken); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateInstance(ctx, ins.AppID, ins.DeploymentID, string(StateWaking), ins.RAMMB, node.ID, uuid.NewString()); !errors.Is(err, ErrNodeCapacity) {
		t.Fatal("fence released unconfirmed destination capacity", err)
	}
	if err := s.RetireFunctionMigrationDestination(ctx, g); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateInstance(ctx, ins.AppID, ins.DeploymentID, string(StateWaking), ins.RAMMB, node.ID, uuid.NewString()); err != nil {
		t.Fatal("confirmed retirement retained capacity", err)
	}
}
