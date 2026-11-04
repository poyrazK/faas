// adr: 20261027090013 step 7 — agentd grants a persist only within the
// account's snapshot quota and with an artifact store to sweep, records the
// snapshot ready only for its own session's reservation, fails a session
// whose workspace is gone, and sweeps expired and deleted snapshots with
// their archives.
package agentd_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/agentd"
	"github.com/onebox-faas/faas/pkg/api"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
	"github.com/onebox-faas/faas/pkg/storage"
)

// persistAnswer is one persist exchange as schedd's side saw it.
type persistAnswer struct {
	grant    *scheddpb.SandboxRunPersistGrant
	recorded *scheddpb.SandboxRunPersistRecorded
}

// persist plays three exchanges: a persist that succeeds, one that fails,
// and a report naming a snapshot the session never reserved.
func (f *fakeSandboxSchedd) persist(stream scheddpb.Schedd_RunSandboxServer) error {
	exchange := func(id string, done func(*scheddpb.SandboxRunPersistGrant) *scheddpb.SandboxRunPersistDone) error {
		if err := stream.Send(&scheddpb.SandboxRunEvent{Frame: &scheddpb.SandboxRunEvent_PersistRequested{
			PersistRequested: &scheddpb.SandboxRunPersistRequested{RequestId: id}}}); err != nil {
			return err
		}
		frame, err := stream.Recv()
		if err != nil {
			return err
		}
		answer := persistAnswer{grant: frame.GetPersistGrant()}
		if answer.grant != nil && len(answer.grant.GetRefusal()) == 0 {
			if err := stream.Send(&scheddpb.SandboxRunEvent{Frame: &scheddpb.SandboxRunEvent_PersistDone{PersistDone: done(answer.grant)}}); err != nil {
				return err
			}
			if frame, err = stream.Recv(); err != nil {
				return err
			}
			answer.recorded = frame.GetPersistRecorded()
		}
		f.mu.Lock()
		f.persists = append(f.persists, answer)
		f.mu.Unlock()
		return nil
	}
	ok := func(g *scheddpb.SandboxRunPersistGrant) *scheddpb.SandboxRunPersistDone {
		return &scheddpb.SandboxRunPersistDone{RequestId: g.GetRequestId(), SnapshotId: g.GetSnapshotId(), Bytes: 512, Sha256: sha}
	}
	failed := func(g *scheddpb.SandboxRunPersistGrant) *scheddpb.SandboxRunPersistDone {
		return &scheddpb.SandboxRunPersistDone{RequestId: g.GetRequestId(), SnapshotId: g.GetSnapshotId(), Failed: true}
	}
	spoofed := func(g *scheddpb.SandboxRunPersistGrant) *scheddpb.SandboxRunPersistDone {
		return &scheddpb.SandboxRunPersistDone{RequestId: g.GetRequestId(), SnapshotId: uuid.NewString(), Bytes: 1, Sha256: sha}
	}
	return errors.Join(exchange("r1", ok), exchange("r2", failed), exchange("r3", spoofed))
}

const sha = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// memStore is an in-memory artifact store.
type memStore struct {
	mu   sync.Mutex
	blob map[string][]byte
}

func (m *memStore) Put(_ context.Context, key string, r io.Reader) error {
	b, err := io.ReadAll(r)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.blob[key] = b
	return err
}

func (m *memStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.blob[key]
	if !ok {
		return nil, storage.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (m *memStore) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.blob[key]; !ok {
		return storage.ErrNotFound
	}
	delete(m.blob, key)
	return nil
}

func TestSandboxPersistGrantsAndRecords(t *testing.T) {
	_, store := openStore(t)
	id := queueSandbox(t, store, 600)
	fake := &fakeSandboxSchedd{scripts: map[string]string{id: "persist"}}
	done := holdSandbox(t, newSandboxes(t, store, fake, agentd.SandboxConfig{Workspaces: &memStore{blob: map[string][]byte{}}}))
	waitFor(t, "three persists", func() bool {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		return len(fake.persists) == 3
	})
	ok, failed, spoofed := fake.persists[0], fake.persists[1], fake.persists[2]
	pro := api.PlanPro.PlanIndex()
	if g := ok.grant; g.GetStorageKey() != agentstore.SandboxSnapshotKey(account, g.GetSnapshotId()) ||
		g.GetMaxBytes() != int64(api.SandboxWorkspaceMaxMB[pro])<<20 || !ok.recorded.GetRecorded() {
		t.Fatalf("persist: %v %v", g, ok.recorded)
	}
	snap, err := store.SandboxSnapshot(context.Background(), account, ok.grant.GetSnapshotId())
	if err != nil || snap.State != agentstore.SandboxSnapshotReady || snap.Bytes != 512 || snap.SHA256 != sha || snap.SandboxID != id ||
		snap.ExpiresAt.Sub(time.Now()) < time.Duration(api.SandboxSnapshotRetentionDays[pro]-1)*24*time.Hour {
		t.Fatalf("recorded snapshot %+v, %v", snap, err)
	}
	if failed.recorded.GetRecorded() {
		t.Fatal("a failed persist was recorded")
	}
	if _, err := store.SandboxSnapshot(context.Background(), account, failed.grant.GetSnapshotId()); !errors.Is(err, agentstore.ErrNotFound) {
		t.Fatalf("a failed persist kept its reservation: %v", err)
	}
	if spoofed.recorded.GetRecorded() {
		t.Fatal("a report of another snapshot was recorded")
	}
	if _, err := store.CloseSandbox(context.Background(), account, id); err != nil {
		t.Fatal(err)
	}
	<-done
}

func TestSandboxPersistRefusedWithoutAnArtifactStore(t *testing.T) {
	_, store := openStore(t)
	id := queueSandbox(t, store, 600)
	fake := &fakeSandboxSchedd{scripts: map[string]string{id: "persist"}}
	done := holdSandbox(t, newSandboxes(t, store, fake, agentd.SandboxConfig{}))
	waitFor(t, "three refusals", func() bool {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		return len(fake.persists) == 3
	})
	var p api.Problem
	if err := json.Unmarshal(fake.persists[0].grant.GetRefusal(), &p); err != nil || p.Code != api.CodeSandboxUnsupported {
		t.Fatalf("refusal %+v, %v", p, err)
	}
	if _, err := store.CloseSandbox(context.Background(), account, id); err != nil {
		t.Fatal(err)
	}
	<-done
}

// readySnapshot reserves and completes a snapshot for a session.
func readySnapshot(t *testing.T, store *agentstore.Store, sandboxID string, expires time.Time) agentstore.SandboxSnapshot {
	t.Helper()
	ctx := context.Background()
	snap, err := store.ReserveSandboxSnapshot(ctx, agentstore.SnapshotReservation{SnapshotID: uuid.NewString(), AccountID: account,
		SandboxID: sandboxID, MaxBytes: 1 << 20, QuotaBytes: 1 << 30, ExpiresAt: expires})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteSandboxSnapshot(ctx, snap.ID, 10, sha); err != nil {
		t.Fatal(err)
	}
	return snap
}

func TestSandboxFromAGoneWorkspaceFails(t *testing.T) {
	ctx := context.Background()
	_, store := openStore(t)
	source := queueSandbox(t, store, 600)
	snap := readySnapshot(t, store, source, time.Now().Add(time.Hour))
	if _, err := store.CloseSandbox(ctx, account, source); err != nil {
		t.Fatal(err)
	}
	sb, err := store.CreateSandbox(ctx, agentstore.SandboxAdmission{AccountID: account, Kind: "ephemeral", Preset: "codex", RAMMB: 1024,
		NetworkMode: "none", IdleTimeoutSec: 600, ExpiresAt: time.Now().Add(time.Hour), QueuedMax: 100, WorkspaceSnapshotID: snap.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RequestSandboxSnapshotDelete(ctx, account, snap.ID); err != nil {
		t.Fatal(err)
	}
	fake := &fakeSandboxSchedd{}
	if !<-holdSandbox(t, newSandboxes(t, store, fake, agentd.SandboxConfig{})) {
		t.Fatal("nothing was claimed")
	}
	if got := sandboxState(t, store, sb.ID); got.State != agentstore.SandboxFailed || got.OutcomeCode != "workspace_unavailable" {
		t.Fatalf("session = %s %s", got.State, got.OutcomeCode)
	}
	if starts, _, _ := fake.counts(); starts != 0 {
		t.Fatalf("a session without its workspace was started %d times", starts)
	}
}

func TestSandboxWorkspaceKeyReachesScheddAndItsLossFails(t *testing.T) {
	ctx := context.Background()
	_, store := openStore(t)
	source := queueSandbox(t, store, 600)
	snap := readySnapshot(t, store, source, time.Now().Add(time.Hour))
	if _, err := store.CloseSandbox(ctx, account, source); err != nil {
		t.Fatal(err)
	}
	sb, err := store.CreateSandbox(ctx, agentstore.SandboxAdmission{AccountID: account, Kind: "ephemeral", Preset: "codex", RAMMB: 1024,
		NetworkMode: "none", IdleTimeoutSec: 600, ExpiresAt: time.Now().Add(time.Hour), QueuedMax: 100, WorkspaceSnapshotID: snap.ID})
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeSandboxSchedd{scripts: map[string]string{sb.ID: "no-workspace"}}
	if !<-holdSandbox(t, newSandboxes(t, store, fake, agentd.SandboxConfig{})) {
		t.Fatal("nothing was claimed")
	}
	if got := fake.starts[0].GetWorkspaceKey(); got != snap.StorageKey {
		t.Fatalf("workspace key %q, want %q", got, snap.StorageKey)
	}
	if got := sandboxState(t, store, sb.ID); got.State != agentstore.SandboxFailed || got.OutcomeCode != "workspace_unavailable" || got.WorkspaceSnapshotID != snap.ID {
		t.Fatalf("session = %+v", got)
	}
}

func TestSandboxSweepDeletesSnapshotsAndArchives(t *testing.T) {
	ctx := context.Background()
	_, store := openStore(t)
	source := queueSandbox(t, store, 600)
	kept := readySnapshot(t, store, source, time.Now().Add(time.Hour))
	expired := readySnapshot(t, store, source, time.Now().Add(-time.Second))
	deleted := readySnapshot(t, store, source, time.Now().Add(time.Hour))
	if err := store.RequestSandboxSnapshotDelete(ctx, account, deleted.ID); err != nil {
		t.Fatal(err)
	}
	blobs := &memStore{blob: map[string][]byte{kept.StorageKey: {1}, expired.StorageKey: {2}, deleted.StorageKey: {3}}}
	sbx := newSandboxes(t, store, &fakeSandboxSchedd{}, agentd.SandboxConfig{Workspaces: blobs})
	if err := sbx.SweepOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(blobs.blob) != 1 || blobs.blob[kept.StorageKey] == nil {
		t.Fatalf("archives left: %v", blobs.blob)
	}
	for _, gone := range []string{expired.ID, deleted.ID} {
		if _, err := store.SandboxSnapshot(ctx, account, gone); !errors.Is(err, agentstore.ErrNotFound) {
			t.Fatalf("snapshot %s kept: %v", gone, err)
		}
	}
	if _, err := store.SandboxSnapshot(ctx, account, kept.ID); err != nil {
		t.Fatal(err)
	}
}
