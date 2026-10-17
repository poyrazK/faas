package sched

// adr: 1353
// Orchestration simulations are not native execution evidence.

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

type restartMigrationState struct {
	destinations                       map[string]state.FunctionMigrationDestination
	inputs                             state.RuntimeConfigInputs
	commitErr, lookupErr, authorityErr error
	commitEffect, missingInputs        bool
	commits, reads, pages              int
	resolution                         state.MigrationCommitResolution
}

func (s *restartMigrationState) ListRestartFunctionMigrationDestinations(_ context.Context, node, after string, limit int) ([]state.FunctionMigrationDestination, error) {
	s.pages++
	var out []state.FunctionMigrationDestination
	for id, d := range s.destinations {
		if id > after && d.Grant.Binding.NodeID == node {
			out = append(out, d.Clone())
		}
	}
	slices.SortFunc(out, func(a, b state.FunctionMigrationDestination) int {
		return cmp.Compare(a.Grant.Binding.InstanceID, b.Grant.Binding.InstanceID)
	})
	return out[:min(limit, len(out))], nil
}
func (s *restartMigrationState) GetFunctionMigrationDestination(_ context.Context, id, token string) (state.FunctionMigrationDestination, error) {
	d, ok := s.destinations[id]
	if !ok || d.Grant.Source.LeaseToken != token {
		return state.FunctionMigrationDestination{}, state.ErrNotFound
	}
	return d.Clone(), nil
}
func (s *restartMigrationState) InstanceRuntimeConfigReceipt(context.Context, string) (state.RuntimeConfigInputs, bool, error) {
	s.reads++
	return s.inputs, !s.missingInputs, s.lookupErr
}
func (s *restartMigrationState) MigrateInstanceOwnerWithRuntimeConfig(_ context.Context, id, from, to, token string, input state.RuntimeConfigMigration) error {
	s.commits++
	d := s.destinations[id]
	g := d.Grant
	if from != g.Source.Parent.Binding.NodeID || to != g.Binding.NodeID || token != g.Source.LeaseToken || input.ExpectedWakeID != g.Source.SourceWakeID ||
		input.WakeID != g.WakeID || input.Inputs == nil || !input.Inputs.Boundary.Equal(s.inputs.Boundary) || input.Inputs.Variables["MODE"] != "captured" ||
		input.Netns != d.Receipt.Netns || input.HostIP != d.Receipt.HostIP || input.GuestUID != int(d.Receipt.LeaseUID) {
		return errors.New("restart reconstructed another epoch or input snapshot")
	}
	if s.commitEffect {
		d.Phase = state.FunctionDestinationCommitted
		s.destinations[id] = d
	}
	return s.commitErr
}
func (s *restartMigrationState) ResolveInstanceMigrationCommit(_ context.Context, a state.MigrationCommitAttempt) (state.MigrationCommitResolution, error) {
	d := s.destinations[a.InstanceID]
	if a.SourceWakeID != d.Grant.Source.SourceWakeID || a.DestinationWakeID != d.Grant.WakeID || a.DestinationNodeID != d.Grant.Binding.NodeID || a.SourceNodeID != d.Grant.Source.Parent.Binding.NodeID || a.LeaseToken != d.Grant.Source.LeaseToken {
		return state.MigrationCommitRetained, state.ErrConflict
	}
	return s.resolution, nil
}
func (s *restartMigrationState) CheckFunctionMigrationDestinationCommitted(_ context.Context, g runtimeadmission.FunctionMigrationDestinationGrant) error {
	if s.authorityErr != nil {
		return s.authorityErr
	}
	if s.destinations[g.Binding.InstanceID].Phase != state.FunctionDestinationCommitted {
		return state.ErrConflict
	}
	return nil
}

type restartMigrationVMM struct {
	*fakeVMM
	store                 *restartMigrationState
	activationErr, ackErr error
}

func (v *restartMigrationVMM) AdoptMigratedInstance(_ context.Context, node, id string, spec AppSpec, memory, vmstate, token string) (LiveMigrationAdopt, error) {
	v.adopts++
	d := v.store.destinations[id]
	if d.Phase != state.FunctionDestinationCommitted || node != d.Grant.Binding.NodeID || token != d.Grant.Source.LeaseToken || memory != d.Grant.Source.MemoryKey || vmstate != d.Grant.Source.VMStateKey || spec.migrationRuntime.WakeID != d.Grant.WakeID {
		return LiveMigrationAdopt{}, state.ErrConflict
	}
	return LiveMigrationAdopt{WakeID: d.Grant.WakeID, Method: vmmdpb.WakeMethod_WAKE_RESTORE, Netns: d.Receipt.Netns, HostIP: d.Receipt.HostIP, GuestUID: int(d.Receipt.LeaseUID)}, v.activationErr
}
func (v *restartMigrationVMM) AcknowledgeMigration(_ context.Context, node, id, token string) error {
	v.acks++
	d := v.store.destinations[id]
	if node != d.Grant.Source.Parent.Binding.NodeID || token != d.Grant.Source.LeaseToken {
		return state.ErrConflict
	}
	if v.ackErr == nil {
		delete(v.store.destinations, id)
	}
	return v.ackErr
}

func restartMigrationFixture(t *testing.T) (*restartMigrationState, *restartMigrationVMM, state.FunctionMigrationDestination) {
	t.Helper()
	_, old, _, _ := snapshotAdmissionBoundaryFixture(t)
	snapshot := old.grant
	now := time.Now()
	source := runtimeadmission.FunctionMigrationGrant{Version: 1, Token: snapshot.Token, Purpose: runtimeadmission.FunctionMigrationCapture, Parent: snapshot.Parent,
		CaptureToken: snapshot.Token, SourceWakeID: uuid.NewString(), LeaseToken: state.FunctionMigrationTokenPrefix + uuid.NewString(),
		LeaseCreatedAtUnixNano: now.UnixNano(), LeaseExpiresAtUnixNano: now.Add(time.Minute).UnixNano(), SourceStartedAtUnixNano: snapshot.SourceStartedAtUnixNano,
		MemoryKey: snapshot.MemoryKey, VMStateKey: snapshot.VMStateKey, PrivateDriveKey: snapshot.PrivateDriveKey, FCVersion: snapshot.FCVersion,
		IssuedAtUnixNano: now.UnixNano(), ExpiresAtUnixNano: now.Add(time.Minute).UnixNano()}
	source.Parent.Binding.InstanceID = "00000000-0000-4000-8000-000000000001"
	c := runtimeadmission.SnapshotCapture{Version: runtimeadmission.SnapshotCaptureVersion, Parent: source.Parent, CapturedAtUnixNano: now.UnixNano(),
		Memory:       runtimeadmission.CapturedArtifact{StorageKey: source.MemoryKey, Digest: snapshot.Parent.ArtifactConsumption.Drives[0].Source.Digest, Bytes: 128 << 20},
		VMState:      runtimeadmission.CapturedArtifact{StorageKey: source.VMStateKey, Digest: snapshot.Parent.ArtifactConsumption.Drives[0].Source.Digest, Bytes: 4096},
		PrivateDrive: runtimeadmission.CapturedArtifact{StorageKey: source.PrivateDriveKey, Digest: snapshot.Parent.ArtifactConsumption.Drives[1].Source.Digest, Bytes: 8192}}
	e := runtimeadmission.SnapshotRestoreEvidence{Version: runtimeadmission.SnapshotRestoreVersion, CaptureToken: source.CaptureToken, FCVersion: source.FCVersion, Capture: c}
	b := source.Parent.Binding
	b.Token, b.NodeID, b.Incarnation = uuid.NewString(), uuid.NewString(), uuid.NewString()
	b.SnapshotCaptureToken, b.SnapshotEvidenceHash = source.CaptureToken, ""
	b.SnapshotEvidenceHash, _ = e.Hash()
	b.IssuedAtUnixNano, b.ExpiresAtUnixNano = now.UnixNano(), now.Add(30*time.Second).UnixNano()
	g := runtimeadmission.FunctionMigrationDestinationGrant{Version: 1, Source: source, Binding: b, Restore: e, WakeID: uuid.NewString()}
	r := source.Parent.Clone()
	r.Binding, r.NativeInputHash, r.Method = b, b.PayloadHash, vmmdpb.WakeMethod_WAKE_RESTORE
	r.ArtifactConsumption.ConfigHash = runtimeadmission.SnapshotLoadCommandHash(false)
	r.CompletedAtUnixNano = now.UnixNano()
	r.SnapshotConsumption = runtimeadmission.SnapshotConsumption{Version: runtimeadmission.SnapshotRestoreVersion, CaptureToken: source.CaptureToken, EvidenceHash: b.SnapshotEvidenceHash,
		Memory: c.Memory, VMState: c.VMState, PrivateDrive: c.PrivateDrive, MappedMemoryBytes: c.Memory.Bytes}
	if err := g.CheckReceipt(r, time.Now()); err != nil {
		t.Fatal("invalid simulated restart fixture", err)
	}
	d := state.FunctionMigrationDestination{Grant: g, Receipt: &r, Phase: state.FunctionDestinationReady}
	s := &restartMigrationState{destinations: map[string]state.FunctionMigrationDestination{b.InstanceID: d}, commitEffect: true,
		inputs: state.RuntimeConfigInputs{Scope: state.DefaultEnvScope, Boundary: now, Variables: map[string]string{"MODE": "captured"}}}
	return s, &restartMigrationVMM{fakeVMM: &fakeVMM{}, store: s}, d
}

func TestFunctionMigrationRestartBoundaries(t *testing.T) {
	for _, name := range []string{"ready", "committed", "commit reply lost", "missing inputs", "input lookup fails", "unresolved commit", "stale authority", "activation fails", "ack fails", "expired readiness"} {
		t.Run(name, func(t *testing.T) {
			s, v, d := restartMigrationFixture(t)
			wantCommit, wantAdopt, wantAck, wantErr := 1, 1, 1, false
			switch name {
			case "committed":
				d.Phase = state.FunctionDestinationCommitted
				wantCommit = 0
				s.lookupErr = errors.New("must not reread committed inputs")
			case "commit reply lost":
				s.commitErr = errors.New("lost commit reply")
				s.resolution = state.MigrationCommitRecovered
			case "missing inputs":
				s.missingInputs = true
				wantCommit, wantAdopt, wantAck, wantErr = 0, 0, 0, true
			case "input lookup fails":
				s.lookupErr = errors.New("offline")
				wantCommit, wantAdopt, wantAck, wantErr = 0, 0, 0, true
			case "unresolved commit":
				s.commitEffect = false
				s.commitErr = state.ErrConflict
				wantAdopt, wantAck, wantErr = 0, 0, true
			case "stale authority":
				d.Phase = state.FunctionDestinationCommitted
				s.authorityErr = state.ErrApplicationStandardRuntimeStale
				wantCommit, wantAdopt, wantAck, wantErr = 0, 0, 0, true
			case "activation fails":
				v.activationErr = errors.New("vmmd unavailable")
				wantAck, wantErr = 0, true
			case "ack fails":
				v.ackErr = errors.New("source unavailable")
				wantErr = true
			case "expired readiness":
				d.Grant.Binding.ExpiresAtUnixNano = time.Now().Add(-time.Second).UnixNano()
				wantCommit, wantAdopt, wantAck, wantErr = 0, 0, 0, true
			}
			s.destinations[d.Grant.Binding.InstanceID] = d
			err := recoverFunctionMigration(t.Context(), s, v, d)
			if (err != nil) != wantErr || s.commits != wantCommit || v.adopts != wantAdopt || v.acks != wantAck || v.cancels != 0 || v.destroys != 0 {
				t.Fatalf("unsafe startup recovery: err=%v commits=%d adopts=%d acks=%d", err, s.commits, v.adopts, v.acks)
			}
			if wantErr && len(s.destinations) != 1 {
				t.Fatal("failed recovery discarded retry authority")
			}
			if name == "committed" && s.reads != 0 {
				t.Fatal("committed epoch rebuilt inputs")
			}
		})
	}
}

func TestFunctionMigrationRestartPaginationSurvivesCleanupAndFailure(t *testing.T) {
	s, v, d := restartMigrationFixture(t)
	d.Phase = state.FunctionDestinationCommitted
	for i := 1; i <= api.MigratingWatchdogTickLimit+1; i++ {
		copy := d.Clone()
		copy.Grant.Binding.InstanceID = fmt.Sprintf("00000000-0000-4000-8000-%012d", i)
		s.destinations[copy.Grant.Binding.InstanceID] = copy
	}
	// The first entry fails closed; successful source cleanup removes later
	// entries. Neither effect may hide the second page.
	bad := s.destinations["00000000-0000-4000-8000-000000000001"]
	bad.Receipt = nil
	s.destinations[bad.Grant.Binding.InstanceID] = bad
	n, err := recoverFunctionMigrations(t.Context(), s, v, d.Grant.Binding.NodeID, testLog())
	if err == nil || n != api.MigratingWatchdogTickLimit || s.pages != 2 || len(s.destinations) != 1 || v.acks != n || s.commits != 0 {
		t.Fatalf("startup pagination lost work: completed=%d pages=%d retained=%d err=%v", n, s.pages, len(s.destinations), err)
	}
}
