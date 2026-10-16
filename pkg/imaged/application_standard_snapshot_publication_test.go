package imaged

// Publication boundary tests simulate catalog history. State tests separately
// cover durable catalog grants and native acceptance measures physical bytes.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

type snapshotCatalogPublicationStore struct {
	*state.MemStore
	record    state.ApplicationStandardSnapshotCaptureRecord
	err       error
	published *state.Snapshot
}

// This boundary fake records the handler's publication arguments. It deliberately
// does not insert its simulated catalog into MemStore's real authority tables.
// Real store publication and raw SQL fences are tested in pkg/state.
func (s *snapshotCatalogPublicationStore) PublishSnapshotIfRuntimeFresh(_ context.Context, snap state.Snapshot, _ string, _ time.Time) (state.Snapshot, error) {
	if s.published != nil {
		return state.Snapshot{}, state.ErrConflict
	}
	snap.ID, snap.CreatedAt = uuid.NewString(), time.Now()
	s.published = &snap
	return snap, nil
}

func (s *snapshotCatalogPublicationStore) LatestSnapshotForTier(_ context.Context, depID, tier string) (state.Snapshot, error) {
	if s.published == nil || s.published.DeploymentID != depID || s.published.Tier != tier {
		return state.Snapshot{}, state.ErrNotFound
	}
	return *s.published, nil
}

func (s *snapshotCatalogPublicationStore) GetApplicationStandardSnapshotCapture(_ context.Context, accountID, appID, depID, token string) (state.ApplicationStandardSnapshotCaptureRecord, error) {
	b := s.record.Grant.Parent.Binding
	if accountID != b.AccountID || appID != b.AppID || depID != b.DeploymentID || token != s.record.Grant.Token {
		return state.ApplicationStandardSnapshotCaptureRecord{}, state.ErrNotFound
	}
	return s.record.Clone(), s.err
}

func snapshotCatalogPublicationFixture(t *testing.T) (*Handler, *snapshotCatalogPublicationStore, state.App, state.Deployment, snapshotWrittenPayload) {
	t.Helper()
	s := &snapshotCatalogPublicationStore{MemStore: state.NewMemStore()}
	account, err := s.CreateAccount(t.Context(), "snapshot-catalog@example.com", "pro")
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "snapshot-catalog", RAMMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	node, err := s.ComputeNodeByName(t.Context(), state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	ins, err := s.CreateInstance(t.Context(), app.ID, dep.ID, string(state.StateParked), 128, node.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	digest := "sha256:" + strings.Repeat("c", 64)
	sources := []runtimeadmission.ArtifactSource{{Kind: "base-image", StorageKey: "base/shared.ext4", Digest: digest, Bytes: 4096}, {Kind: "app-layer", StorageKey: "apps/main.ext4", Digest: digest, Bytes: 8192}}
	sourceHash, _ := runtimeadmission.HashArtifactSources(sources)
	b := runtimeadmission.Binding{ProtocolVersion: runtimeadmission.ArtifactProtocolVersion, Token: uuid.NewString(), InstanceID: standardSnapshotScopeUUID(ins.ID), AccountID: standardSnapshotScopeUUID(account.ID), AppID: standardSnapshotScopeUUID(app.ID), DeploymentID: standardSnapshotScopeUUID(dep.ID), NodeID: standardSnapshotScopeUUID(node.ID), Incarnation: uuid.NewString(), DesiredRevision: 1, EffectiveHash: strings.Repeat("a", 64), CapturedInputHash: strings.Repeat("b", 64), PayloadHash: strings.Repeat("c", 64), ArtifactSourcesHash: sourceHash, EgressRevision: 1, IssuedAtUnixNano: now.UnixNano(), ExpiresAtUnixNano: now.Add(time.Minute).UnixNano()}
	consumption := runtimeadmission.ArtifactConsumption{ConfigHash: strings.Repeat("d", 64), ProcessPID: 42, ProcessStart: "101"}
	for _, a := range sources {
		consumption.Drives = append(consumption.Drives, runtimeadmission.ConsumedDrive{Source: a, DriveID: a.Role(), ReadOnly: a.Role() == "base", RootDevice: a.Role() == "base", ProducerDigest: a.Digest, ProducerBytes: a.Bytes, InjectedDigest: a.Digest, InjectedBytes: a.Bytes})
	}
	parent := runtimeadmission.Receipt{Binding: b, NativeInputHash: strings.Repeat("d", 64), Netns: "fc-snapshot", HostIP: "10.100.0.2", LeaseUID: 20000, Method: vmmdpb.WakeMethod_WAKE_COLD_BOOT, CompletedAtUnixNano: now.UnixNano(), ArtifactConsumption: consumption}
	token := uuid.NewString()
	key := state.SnapshotCaptureMemKey(dep.ID, state.SnapshotTierWarm, token)
	g := runtimeadmission.SnapshotGrant{Version: runtimeadmission.SnapshotGrantVersion, Token: token, Parent: parent, MemoryKey: key, VMStateKey: state.SnapshotVMStateKey(state.Snapshot{StorageKey: key}), PrivateDriveKey: state.SnapshotDriveKey(state.Snapshot{StorageKey: key}), FCVersion: "1.10.0", Mode: "warm", SourceStartedAtUnixNano: ins.StartedAt.UnixNano(), IssuedAtUnixNano: now.UnixNano(), ExpiresAtUnixNano: now.Add(time.Minute).UnixNano()}
	c := runtimeadmission.SnapshotCapture{Version: runtimeadmission.SnapshotCaptureVersion, Parent: parent.Clone(), Memory: runtimeadmission.CapturedArtifact{StorageKey: g.MemoryKey, Digest: digest, Bytes: 128 << 20}, VMState: runtimeadmission.CapturedArtifact{StorageKey: g.VMStateKey, Digest: digest, Bytes: 4096}, PrivateDrive: runtimeadmission.CapturedArtifact{StorageKey: g.PrivateDriveKey, Digest: digest, Bytes: 8192}, CapturedAtUnixNano: now.UnixNano()}
	a := runtimeadmission.SnapshotAcknowledgment{Grant: g.Clone(), Capture: c, CompletedAtUnixNano: now.UnixNano()}
	if err := a.Check(g, now); err != nil {
		t.Fatal(err)
	}
	s.record = state.ApplicationStandardSnapshotCaptureRecord{ExpectedState: string(state.StateRunning), Grant: g, Acknowledgment: &a, CreatedAt: now, ReceivedAt: now}
	p := snapshotWrittenPayload{DeploymentID: dep.ID, SourceInstanceID: ins.ID, SourceStartedAt: ins.StartedAt, NodeID: node.ID, StorageKey: key, ApplicationStandardCaptureToken: token, FCVersion: g.FCVersion, Tier: state.SnapshotTierWarm, MemBytes: c.Memory.Bytes, VMStateBytes: c.VMState.Bytes, StoredBytes: 4096}
	return New(s, &fakeNotifier{}, fakePuller{}, &fakeBuilder{}, "./init", t.TempDir(), silentLogger()), s, app, dep, p
}

func TestStandardSnapshotPublicationRecordsOnlyMatchingCatalogReference(t *testing.T) {
	h, s, _, dep, p := snapshotCatalogPublicationFixture(t)
	for range 2 {
		if err := h.handleSnapshotWritten(t.Context(), p); err != nil {
			t.Fatal(err)
		}
	}
	snap, err := s.LatestSnapshotForTier(t.Context(), dep.ID, state.SnapshotTierWarm)
	if err != nil || snap.ApplicationStandardCaptureToken != s.record.Grant.Token || snap.StorageKey != s.record.Grant.MemoryKey || snap.MemBytes != s.record.Acknowledgment.Capture.Memory.Bytes {
		t.Fatal("matching catalog reference was not published", err)
	}
}

func TestStandardSnapshotPublicationRejectsUnpublishedAndMismatchedHistory(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*snapshotCatalogPublicationStore, *snapshotWrittenPayload)
	}{
		{"unknown token", func(_ *snapshotCatalogPublicationStore, p *snapshotWrittenPayload) {
			p.ApplicationStandardCaptureToken = uuid.NewString()
		}},
		{"missing token", func(_ *snapshotCatalogPublicationStore, p *snapshotWrittenPayload) {
			p.ApplicationStandardCaptureToken = ""
		}},
		{"source", func(_ *snapshotCatalogPublicationStore, p *snapshotWrittenPayload) {
			p.SourceInstanceID = uuid.NewString()
		}},
		{"source start", func(_ *snapshotCatalogPublicationStore, p *snapshotWrittenPayload) {
			p.SourceStartedAt = p.SourceStartedAt.Add(time.Second)
		}},
		{"node", func(_ *snapshotCatalogPublicationStore, p *snapshotWrittenPayload) { p.NodeID = uuid.NewString() }},
		{"key", func(_ *snapshotCatalogPublicationStore, p *snapshotWrittenPayload) {
			p.StorageKey = state.SnapshotCaptureMemKey(p.DeploymentID, p.Tier, uuid.NewString())
		}},
		{"Firecracker", func(_ *snapshotCatalogPublicationStore, p *snapshotWrittenPayload) { p.FCVersion += "-other" }},
		{"tier", func(_ *snapshotCatalogPublicationStore, p *snapshotWrittenPayload) { p.Tier = state.SnapshotTierInit }},
		{"memory", func(_ *snapshotCatalogPublicationStore, p *snapshotWrittenPayload) { p.MemBytes++ }},
		{"vmstate", func(_ *snapshotCatalogPublicationStore, p *snapshotWrittenPayload) { p.VMStateBytes++ }},
		{"unpublished", func(s *snapshotCatalogPublicationStore, _ *snapshotWrittenPayload) { s.record.Acknowledgment = nil }},
		{"invalid ACK", func(s *snapshotCatalogPublicationStore, _ *snapshotWrittenPayload) {
			s.record.Acknowledgment.Capture.PrivateDrive.Bytes++
		}},
		{"catalog unavailable", func(s *snapshotCatalogPublicationStore, _ *snapshotWrittenPayload) {
			s.err = runtimeadmission.ErrUnavailable
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, s, _, dep, p := snapshotCatalogPublicationFixture(t)
			tc.edit(s, &p)
			if err := h.handleSnapshotWritten(t.Context(), p); err == nil {
				t.Fatal("invalid capture notification published")
			}
			if _, err := s.LatestSnapshotForTier(t.Context(), dep.ID, state.SnapshotTierWarm); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("rejected capture left a usable snapshot row", err)
			}
		})
	}
}

func TestStandardSnapshotHistoricalPublicationDoesNotRenewCaptureGrant(t *testing.T) {
	h, s, app, dep, p := snapshotCatalogPublicationFixture(t)
	old := int64(time.Hour)
	g := &s.record.Grant
	g.IssuedAtUnixNano -= old
	g.ExpiresAtUnixNano -= old
	g.SourceStartedAtUnixNano -= old
	g.Parent.Binding.IssuedAtUnixNano -= old
	g.Parent.Binding.ExpiresAtUnixNano -= old
	g.Parent.CompletedAtUnixNano -= old
	a := s.record.Acknowledgment
	a.Grant = g.Clone()
	a.Capture.Parent = g.Parent.Clone()
	a.Capture.CapturedAtUnixNano -= old
	a.CompletedAtUnixNano -= old
	p.SourceStartedAt = p.SourceStartedAt.Add(-time.Hour)
	if err := h.verifyStandardSnapshotWritten(t.Context(), app, dep, p); err != nil {
		t.Fatal("expiry erased immutable history", err)
	}
	if err := g.Validate(time.Now()); !errors.Is(err, runtimeadmission.ErrExpired) {
		t.Fatal("historical publication renewed capture authority", err)
	}
}
