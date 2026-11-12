// adr: 20261027090013 step 7 — a session starts from a persisted workspace
// only when the snapshot is the account's, ready, unexpired and not being
// deleted; the account lists, reads and deletes its snapshots, and a deleted
// one is gone at once.
package sandboxes_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

const (
	readySnapshot   = "33333333-3333-4333-8333-333333333333"
	pendingSnapshot = "44444444-4444-4444-8444-444444444444"
	unknownSnapshot = "55555555-5555-4555-8555-555555555555"
)

func (f *fakeStore) usable(accountID, id string) bool {
	for _, s := range f.snapshots {
		if s.ID == id {
			return s.AccountID == accountID && s.State == agentstore.SandboxSnapshotReady && s.DeleteRequestedAt == nil &&
				s.ExpiresAt.After(f.now())
		}
	}
	return false
}

func (f *fakeStore) SandboxSnapshot(_ context.Context, accountID, id string) (agentstore.SandboxSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.snapshots {
		if s.ID == id && s.AccountID == accountID {
			return s, nil
		}
	}
	return agentstore.SandboxSnapshot{}, agentstore.ErrNotFound
}

func (f *fakeStore) SandboxSnapshots(_ context.Context, accountID string, filter agentstore.SandboxSnapshotFilter) ([]agentstore.SandboxSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []agentstore.SandboxSnapshot
	started := filter.Before == nil
	for _, s := range f.snapshots {
		if !started {
			started = s.ID == filter.Before.ID
			continue
		}
		if s.AccountID == accountID && s.DeleteRequestedAt == nil && len(out) < filter.Limit {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *fakeStore) RequestSandboxSnapshotDelete(_ context.Context, accountID, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, s := range f.snapshots {
		if s.ID == id && s.AccountID == accountID {
			if s.DeleteRequestedAt == nil {
				now := f.now()
				f.snapshots[i].DeleteRequestedAt = &now
			}
			return nil
		}
	}
	return agentstore.ErrNotFound
}

func withSnapshots(f *fixture) {
	ready := f.now.Add(-time.Minute)
	f.store.snapshots = []agentstore.SandboxSnapshot{
		{ID: pendingSnapshot, AccountID: account, State: agentstore.SandboxSnapshotPending, ExpiresAt: f.now.Add(time.Hour), CreatedAt: f.now},
		{ID: readySnapshot, AccountID: account, SandboxID: "00000000-0000-0000-0000-000000000009", State: agentstore.SandboxSnapshotReady,
			Bytes: 4096, SHA256: "ab", ExpiresAt: f.now.Add(time.Hour), CreatedAt: ready, ReadyAt: &ready},
	}
}

func TestCreateFromASnapshot(t *testing.T) {
	f := newFixture(t)
	withSnapshots(f)
	sb, code := f.create(t, pro(), `{"preset":"codex","snapshot_id":"`+readySnapshot+`"}`)
	if code != http.StatusCreated || sb.SnapshotID != readySnapshot {
		t.Fatalf("create from a ready snapshot = %d %+v", code, sb)
	}
	claims, err := f.signer.Verify(sb.Token, f.now)
	if err != nil || claims.WorkspaceMaxBytes != int64(api.SandboxWorkspaceMaxMB[api.PlanPro.PlanIndex()])<<20 {
		t.Fatalf("token carries workspace cap %d, %v", claims.WorkspaceMaxBytes, err)
	}
	var p api.Problem
	if code := f.call(t, f.h.Create, pro(), http.MethodPost, "/v1/sandboxes", `{"preset":"codex","snapshot_id":"`+pendingSnapshot+`"}`, "", &p); code != http.StatusNotFound || p.Code != api.CodeSandboxSnapshotNotFound {
		t.Fatalf("create from a pending snapshot = %d %q", code, p.Code)
	}
}

func TestSnapshotRoutes(t *testing.T) {
	f := newFixture(t)
	withSnapshots(f)
	var list api.SandboxSnapshotList
	if code := f.call(t, f.h.ListSnapshots, pro(), http.MethodGet, "/v1/sandbox-snapshots?limit=1", "", "", &list); code != http.StatusOK ||
		len(list.Items) != 1 || list.Items[0].ID != pendingSnapshot || list.NextBefore != pendingSnapshot {
		t.Fatalf("first page = %d %+v", code, list)
	}
	list = api.SandboxSnapshotList{}
	if code := f.call(t, f.h.ListSnapshots, pro(), http.MethodGet, "/v1/sandbox-snapshots?before="+pendingSnapshot, "", "", &list); code != http.StatusOK ||
		len(list.Items) != 1 || list.Items[0].ID != readySnapshot || list.Items[0].Bytes != 4096 || list.NextBefore != "" {
		t.Fatalf("second page = %d %+v", code, list)
	}
	var snap api.SandboxSnapshot
	if code := f.call(t, f.h.GetSnapshot, pro(), http.MethodGet, "/v1/sandbox-snapshots/"+readySnapshot, "", readySnapshot, &snap); code != http.StatusOK || snap.State != "ready" {
		t.Fatalf("get = %d %+v", code, snap)
	}
	for range 2 {
		if code := f.call(t, f.h.DeleteSnapshot, pro(), http.MethodDelete, "/v1/sandbox-snapshots/"+readySnapshot, "", readySnapshot, nil); code != http.StatusNoContent {
			t.Fatalf("delete = %d", code)
		}
	}
	var p api.Problem
	if code := f.call(t, f.h.GetSnapshot, pro(), http.MethodGet, "/v1/sandbox-snapshots/"+readySnapshot, "", readySnapshot, &p); code != http.StatusNotFound || p.Code != api.CodeSandboxSnapshotNotFound {
		t.Fatalf("get after delete = %d %q", code, p.Code)
	}
	if code := f.call(t, f.h.Create, pro(), http.MethodPost, "/v1/sandboxes", `{"preset":"codex","snapshot_id":"`+readySnapshot+`"}`, "", &p); code != http.StatusNotFound {
		t.Fatalf("create from a deleted snapshot = %d", code)
	}
	if code := f.call(t, f.h.DeleteSnapshot, pro(), http.MethodDelete, "/v1/sandbox-snapshots/"+unknownSnapshot, "", unknownSnapshot, &p); code != http.StatusNotFound {
		t.Fatalf("delete unknown = %d", code)
	}
	if len(f.audits) != 2 || f.audits[0] != "sandbox.snapshot.deleted" {
		t.Fatalf("audits %v", f.audits)
	}
}
