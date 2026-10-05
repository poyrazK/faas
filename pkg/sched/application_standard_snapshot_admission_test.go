// adr: 590
package sched

// Orchestration tests simulate native/captured-store consumers. Durable catalog
// storage and native physical-byte acceptance remain independent gates.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

type snapshotAdmissionBoundaryStore struct {
	*state.MemStore
	grant                            runtimeadmission.SnapshotGrant
	issued, published                bool
	issueErr, publishErr, receiptErr error
}

func (s *snapshotAdmissionBoundaryStore) GetInstanceApplicationStandardAdmission(context.Context, string) (state.InstanceApplicationStandardAdmission, error) {
	return state.InstanceApplicationStandardAdmission{Managed: true}, nil
}
func (s *snapshotAdmissionBoundaryStore) GetInstanceApplicationStandardRuntimeReceipt(context.Context, string) (runtimeadmission.Receipt, error) {
	return s.grant.Parent.Clone(), s.receiptErr
}
func (s *snapshotAdmissionBoundaryStore) IssueApplicationStandardSnapshotCapture(_ context.Context, expected string, r state.ApplicationStandardSnapshotCaptureRequest) (runtimeadmission.SnapshotGrant, error) {
	if expected != string(state.StateRunning) || r.Token != s.grant.Token || r.InstanceID != s.grant.Parent.Binding.InstanceID || r.MemoryKey != s.grant.MemoryKey || r.VMStateKey != s.grant.VMStateKey || r.PrivateDriveKey != s.grant.PrivateDriveKey || r.FCVersion != s.grant.FCVersion || r.Mode != s.grant.Mode || r.SourceStartedAtUnixNano != s.grant.SourceStartedAtUnixNano {
		return runtimeadmission.SnapshotGrant{}, runtimeadmission.ErrInvalid
	}
	if s.issueErr != nil {
		return runtimeadmission.SnapshotGrant{}, s.issueErr
	}
	s.issued = true
	return s.grant.Clone(), nil
}
func (s *snapshotAdmissionBoundaryStore) PublishApplicationStandardSnapshotCapture(_ context.Context, a runtimeadmission.SnapshotAcknowledgment) error {
	if !s.issued || a.Check(s.grant, time.Now()) != nil {
		return runtimeadmission.ErrInvalid
	}
	if s.publishErr != nil {
		return s.publishErr
	}
	s.published = true
	return nil
}

type snapshotAdmissionBoundaryVMM struct {
	*fakeVMM
	store    *snapshotAdmissionBoundaryStore
	captures int
	edit     func(*SnapshotBytes, *runtimeadmission.SnapshotAcknowledgment)
}

func (v *snapshotAdmissionBoundaryVMM) CaptureAdmittedRuntime(_ context.Context, node string, g runtimeadmission.SnapshotGrant) (SnapshotBytes, runtimeadmission.SnapshotAcknowledgment, error) {
	v.captures++
	if !v.store.issued || !v.store.grant.Equal(g) || node != g.Parent.Binding.NodeID {
		return SnapshotBytes{}, runtimeadmission.SnapshotAcknowledgment{}, runtimeadmission.ErrInvalid
	}
	now := time.Now().UnixNano()
	digest := "sha256:" + strings.Repeat("d", 64)
	c := runtimeadmission.SnapshotCapture{Version: runtimeadmission.SnapshotCaptureVersion, Parent: g.Parent.Clone(), Memory: runtimeadmission.CapturedArtifact{StorageKey: g.MemoryKey, Digest: digest, Bytes: 128 << 20}, VMState: runtimeadmission.CapturedArtifact{StorageKey: g.VMStateKey, Digest: digest, Bytes: 4096}, PrivateDrive: runtimeadmission.CapturedArtifact{StorageKey: g.PrivateDriveKey, Digest: digest, Bytes: 8192}, CapturedAtUnixNano: now}
	b := SnapshotBytes{MemBytes: c.Memory.Bytes, VMStateBytes: c.VMState.Bytes, StoredBytes: 4096, Capture: c.Clone()}
	a := runtimeadmission.SnapshotAcknowledgment{Grant: g.Clone(), Capture: c, CompletedAtUnixNano: now}
	if v.edit != nil {
		v.edit(&b, &a)
	}
	return b, a, nil
}

func snapshotAdmissionBoundaryFixture(t *testing.T) (*Engine, *snapshotAdmissionBoundaryStore, *snapshotAdmissionBoundaryVMM, state.Instance) {
	t.Helper()
	base, app, dep := newStandardCaptureService(t)
	node := standardNativeTestNodeID(t, base)
	now := time.Now()
	ins := state.Instance{ID: uuid.NewString(), AppID: app.ID, DeploymentID: dep.ID, State: string(state.StateRunning), RAMMB: 128, NodeID: node, StartedAt: now}
	digest := "sha256:" + strings.Repeat("c", 64)
	sources := []runtimeadmission.ArtifactSource{{Kind: "base-image", StorageKey: "base", Digest: digest, Bytes: 4096}, {Kind: "app-layer", StorageKey: "layer", Digest: digest, Bytes: 8192}}
	hash, _ := runtimeadmission.HashArtifactSources(sources)
	b := runtimeadmission.Binding{ProtocolVersion: runtimeadmission.ArtifactProtocolVersion, Token: uuid.NewString(), InstanceID: ins.ID, AppID: canonicalStandardNativeUUID(app.ID), AccountID: canonicalStandardNativeUUID(app.AccountID), DeploymentID: canonicalStandardNativeUUID(dep.ID), NodeID: node, Incarnation: uuid.NewString(), DesiredRevision: 1, EffectiveHash: strings.Repeat("a", 64), CapturedInputHash: strings.Repeat("b", 64), PayloadHash: strings.Repeat("c", 64), ArtifactSourcesHash: hash, EgressRevision: 1, IssuedAtUnixNano: now.UnixNano(), ExpiresAtUnixNano: now.Add(time.Minute).UnixNano()}
	c := runtimeadmission.ArtifactConsumption{ConfigHash: strings.Repeat("d", 64), ProcessPID: 42, ProcessStart: "101"}
	for _, a := range sources {
		c.Drives = append(c.Drives, runtimeadmission.ConsumedDrive{Source: a, DriveID: a.Role(), ReadOnly: a.Role() == "base", RootDevice: a.Role() == "base", ProducerDigest: a.Digest, ProducerBytes: a.Bytes, InjectedDigest: a.Digest, InjectedBytes: a.Bytes})
	}
	parent := runtimeadmission.Receipt{Binding: b, NativeInputHash: strings.Repeat("d", 64), Netns: "fc-snapshot", HostIP: "10.100.0.2", LeaseUID: 20000, Method: vmmdpb.WakeMethod_WAKE_COLD_BOOT, CompletedAtUnixNano: now.UnixNano(), ArtifactConsumption: c}
	token := uuid.NewString()
	key := state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierWarm, token)
	g := runtimeadmission.SnapshotGrant{Version: runtimeadmission.SnapshotGrantVersion, Token: token, Parent: parent, MemoryKey: key, VMStateKey: state.SnapshotVMStateKey(state.Snapshot{StorageKey: key}), PrivateDriveKey: state.SnapshotDriveKey(state.Snapshot{StorageKey: key}), FCVersion: "1.10.0", Mode: "warm", SourceStartedAtUnixNano: ins.StartedAt.UnixNano(), IssuedAtUnixNano: now.UnixNano(), ExpiresAtUnixNano: now.Add(time.Minute).UnixNano()}
	if err := g.Validate(now); err != nil {
		t.Fatal(err)
	}
	s := &snapshotAdmissionBoundaryStore{MemStore: base, grant: g}
	v := &snapshotAdmissionBoundaryVMM{fakeVMM: &fakeVMM{}, store: s}
	return newEngine(t, s, v, &fakeNotifier{}, g.FCVersion), s, v, ins
}

func TestStandardSnapshotAdmissionSavesGrantAndAcknowledgmentBeforePublication(t *testing.T) {
	e, s, v, ins := snapshotAdmissionBoundaryFixture(t)
	g := s.grant
	b, err := e.captureSnapshotWithStandards(t.Context(), ins, "", g.MemoryKey, g.VMStateKey, false, "warm")
	if err != nil || !s.issued || !s.published || v.captures != 1 || v.snapshots != 0 || b.CaptureToken != g.Token || b.Capture.Check(time.Now()) != nil {
		t.Fatal("managed capture bypassed durable ordering", err)
	}
}

func TestStandardSnapshotAdmissionRejectsFailedBoundariesWithoutLegacyFallback(t *testing.T) {
	for _, tc := range []struct {
		name  string
		edit  func(*snapshotAdmissionBoundaryStore, *snapshotAdmissionBoundaryVMM)
		calls int
	}{
		{"receipt stale", func(s *snapshotAdmissionBoundaryStore, _ *snapshotAdmissionBoundaryVMM) {
			s.receiptErr = state.ErrApplicationStandardRuntimeStale
		}, 0},
		{"grant refused", func(s *snapshotAdmissionBoundaryStore, _ *snapshotAdmissionBoundaryVMM) {
			s.issueErr = state.ErrApplicationStandardRuntimeStale
		}, 0},
		{"ACK refused", func(s *snapshotAdmissionBoundaryStore, _ *snapshotAdmissionBoundaryVMM) {
			s.publishErr = state.ErrApplicationStandardRuntimeStale
		}, 1},
		{"missing capture", func(_ *snapshotAdmissionBoundaryStore, v *snapshotAdmissionBoundaryVMM) {
			v.edit = func(b *SnapshotBytes, _ *runtimeadmission.SnapshotAcknowledgment) {
				b.Capture = runtimeadmission.SnapshotCapture{}
			}
		}, 1},
		{"wrong count", func(_ *snapshotAdmissionBoundaryStore, v *snapshotAdmissionBoundaryVMM) {
			v.edit = func(b *SnapshotBytes, _ *runtimeadmission.SnapshotAcknowledgment) { b.MemBytes++ }
		}, 1},
		{"wrong grant", func(_ *snapshotAdmissionBoundaryStore, v *snapshotAdmissionBoundaryVMM) {
			v.edit = func(_ *SnapshotBytes, a *runtimeadmission.SnapshotAcknowledgment) { a.Grant.Token = uuid.NewString() }
		}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, s, v, ins := snapshotAdmissionBoundaryFixture(t)
			tc.edit(s, v)
			g := s.grant
			b, err := e.captureSnapshotWithStandards(t.Context(), ins, "", g.MemoryKey, g.VMStateKey, false, "warm")
			if err == nil || s.published || !b.Capture.IsZero() || b.CaptureToken != "" || v.snapshots != 0 || v.captures != tc.calls {
				t.Fatal("failed boundary returned publication evidence or fell back", err)
			}
		})
	}
}

func TestStandardSnapshotAdmissionRequiresMeasuredConsumerBeforeIssuing(t *testing.T) {
	e, s, v, ins := snapshotAdmissionBoundaryFixture(t)
	e.vmm = v.fakeVMM
	g := s.grant
	if _, err := e.captureSnapshotWithStandards(t.Context(), ins, "", g.MemoryKey, g.VMStateKey, false, "warm"); !errors.Is(err, runtimeadmission.ErrUnavailable) || s.issued || v.snapshots != 0 {
		t.Fatal("old consumer received measured capture authority", err)
	}
}

func TestStandardSnapshotWrittenCarriesOnlyDurableCatalogReference(t *testing.T) {
	e, s, _, ins := snapshotAdmissionBoundaryFixture(t)
	g := s.grant
	b, err := e.captureSnapshotWithStandards(t.Context(), ins, "", g.MemoryKey, g.VMStateKey, false, "warm")
	if err != nil {
		t.Fatal(err)
	}
	n := &fakeNotifier{}
	e.notif = n
	e.emitSnapshotWritten(t.Context(), ins.ID, ins.StartedAt, ins.DeploymentID, ins.NodeID, "", g.MemoryKey, b, state.SnapshotTierWarm, nil)
	if len(n.events) != 1 || n.events[0].channel != db.NotifySnapshotWritten {
		t.Fatal("catalog publication did not produce its notification")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(n.events[0].payload), &fields); err != nil {
		t.Fatal(err)
	}
	var token string
	if json.Unmarshal(fields["application_standard_capture_token"], &token) != nil || token != g.Token {
		t.Fatal("notification lost its exact catalog reference")
	}
	for _, name := range []string{"capture", "grant", "acknowledgment", "parent"} {
		if fields[name] != nil {
			t.Fatal("notification carried full native authority or capture evidence")
		}
	}
}
