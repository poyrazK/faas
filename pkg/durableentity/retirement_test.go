package durableentity

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func retirementRequest(t *testing.T, f fixture) RetirementRequest {
	t.Helper()
	p, err := f.manager.PreviewRetirement(t.Context(), f.id)
	if err != nil {
		t.Fatal(err)
	}
	return RetirementRequest{OperationID: uuid.NewString(), Version: p.Version, Revision: p.RecoveryRevision}
}
func finishRetirement(t *testing.T, f fixture, m *Manager, status *RetirementStatus) *RetirementStatus {
	t.Helper()
	f.clock.Store(status.NotBefore.UnixNano())
	for steps := 0; status.Phase != "complete"; steps++ {
		if steps > 100 {
			t.Fatal("retirement did not complete")
		}
		var err error
		status, err = m.StepRetirement(t.Context(), f.id, status.Request.OperationID, status.Revision)
		if err != nil {
			t.Fatal(err)
		}
	}
	return status
}
func TestRetirementFencesIdentityPreservesReceiptsAndReclaimsObsoleteState(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < 50; i++ {
		if _, err := f.manager.Execute(t.Context(), f.claim, request(uuid.NewString()), increment); err != nil {
			t.Fatal(err)
		}
	}
	base, _, err := f.manager.readManifest(t.Context(), f.id)
	if err != nil {
		t.Fatal(err)
	}
	oldSnapshot := base.SnapshotKey
	state, err := f.manager.readSnapshot(t.Context(), base)
	if err != nil {
		t.Fatal(err)
	}
	originalRoot := *state.ReceiptRoot
	if err = f.manager.Release(t.Context(), f.claim); err != nil {
		t.Fatal(err)
	}
	p := retirementRequest(t, f)
	preview, err := f.manager.PreviewRetirement(t.Context(), f.id)
	if err != nil || !preview.Ready || preview.Receipts != 50 {
		t.Fatal(preview, err)
	}
	status, err := f.manager.StartRetirement(t.Context(), f.id, p, "operator")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.manager.Acquire(t.Context(), f.id, "new-owner"); !errors.Is(err, ErrRetired) {
		t.Fatal("identity resurrected", err)
	}
	if _, err = f.manager.Renew(t.Context(), f.claim); !errors.Is(err, ErrRetired) {
		t.Fatal("old owner renewed", err)
	}
	called := false
	if _, err = f.manager.Execute(t.Context(), f.claim, request("after-retirement"), func(ctx context.Context, view View) (Transition, error) { called = true; return increment(ctx, view) }); !errors.Is(err, ErrRetired) || called {
		t.Fatal("retired owner dispatched", err)
	}
	if _, err = f.manager.StepRetirement(t.Context(), f.id, p.OperationID, status.Revision); !errors.Is(err, ErrBusy) {
		t.Fatal("drain bypassed", err)
	}
	replay, err := f.manager.StartRetirement(t.Context(), f.id, p, "another-authorized-actor")
	if err != nil || replay.Actor != "operator" || replay.Revision != status.Revision {
		t.Fatal(replay, err)
	}
	changed := p
	changed.OperationID = uuid.NewString()
	if _, err = f.manager.StartRetirement(t.Context(), f.id, changed, "operator"); !errors.Is(err, ErrRequestConflict) {
		t.Fatal(err)
	}
	status = finishRetirement(t, f, openManager(t, f.store, f.clock), status)
	base, _, err = f.manager.readManifest(t.Context(), f.id)
	if err != nil {
		t.Fatal(err)
	}
	state, err = f.manager.readSnapshot(t.Context(), base)
	if err != nil || string(state.Data) != "{}" || *state.ReceiptRoot != originalRoot || base.StorageUsage.ReceiptCount != 50 || status.RetainedBytes != base.StorageUsage.TotalBytes() {
		t.Fatal(state, status, err)
	}
	if _, _, err = f.store.Get(t.Context(), oldSnapshot, 1<<20); !errors.Is(err, ErrNotFound) {
		t.Fatal("old application state retained", err)
	}
	proof, err := f.manager.startInventory(t.Context(), base, state)
	if err != nil {
		t.Fatal(err)
	}
	for !proof.LogicalComplete {
		if err = f.manager.inventoryJournalPage(t.Context(), base, &proof); err != nil {
			t.Fatal(err)
		}
	}
	if !sameUsage(proof.Usage, *base.StorageUsage) {
		t.Fatal("retained receipt graph damaged")
	}
	if _, err = f.manager.StepRetirement(t.Context(), f.id, p.OperationID, 1); !errors.Is(err, ErrConflict) {
		t.Fatal("stale checkpoint accepted", err)
	}
}
func TestRetirementBlocksLiveOwnersAlarmsAndStaleObservations(t *testing.T) {
	for _, mode := range []string{"owner", "alarm", "stale"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			p := retirementRequest(t, f)
			if mode == "alarm" {
				_, err := f.manager.Execute(t.Context(), f.claim, request("alarm"), func(ctx context.Context, view View) (Transition, error) {
					out, err := increment(ctx, view)
					at := f.manager.now()
					out.AlarmAt = &at
					return out, err
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			if mode != "owner" {
				if err := f.manager.Release(t.Context(), f.claim); err != nil {
					t.Fatal(err)
				}
				if mode == "alarm" {
					p = retirementRequest(t, f)
				}
			}
			_, err := f.manager.StartRetirement(t.Context(), f.id, p, "operator")
			want := ErrBusy
			if mode == "stale" {
				want = ErrConflict
			}
			if !errors.Is(err, want) {
				t.Fatal(err)
			}
			status, err := f.manager.RetirementStatus(t.Context(), f.id)
			if err != nil || status != nil {
				t.Fatal("blocked retirement wrote tombstone", status, err)
			}
		})
	}
}
func TestRetirementUnknownManifestAndDeleteResumeWithoutLosingReceipts(t *testing.T) {
	f := newFixture(t)
	if _, err := f.manager.Execute(t.Context(), f.claim, request("one"), increment); err != nil {
		t.Fatal(err)
	}
	if err := f.manager.Release(t.Context(), f.claim); err != nil {
		t.Fatal(err)
	}
	p := retirementRequest(t, f)
	lost := false
	store := cleanupFaultStore{memoryStore: f.store, put: func(ctx context.Context, key string, body []byte, etag string) (string, error) {
		v, err := f.store.Put(ctx, key, body, etag)
		if err == nil && !lost && strings.HasSuffix(key, "/manifest.json") {
			lost = true
			return "", errors.New("committed acknowledgement lost")
		}
		return v, err
	}}
	m := openManager(t, store, f.clock)
	if status, err := m.StartRetirement(t.Context(), f.id, p, "operator"); status != nil || !errors.Is(err, ErrUncertain) {
		t.Fatal(status, err)
	}
	status, err := m.StartRetirement(t.Context(), f.id, p, "operator")
	if err != nil {
		t.Fatal(err)
	}
	f.clock.Store(status.NotBefore.UnixNano())
	for status.Phase == "proof" {
		status, err = m.StepRetirement(t.Context(), f.id, p.OperationID, status.Revision)
		if err != nil {
			t.Fatal(err)
		}
	}
	lostDelete := false
	store.remove = func(ctx context.Context, key string) error {
		if err := f.store.DeleteEntityObject(ctx, key); err != nil {
			return err
		}
		if !lostDelete {
			lostDelete = true
			return errors.New("delete acknowledgement lost")
		}
		return nil
	}
	m = openManager(t, store, f.clock)
	before := status.Revision
	if _, err = m.StepRetirement(t.Context(), f.id, p.OperationID, before); !errors.Is(err, ErrUncertain) {
		t.Fatal(err)
	}
	status, err = m.RetirementStatus(t.Context(), f.id)
	if err != nil || status.Revision != before {
		t.Fatal(status, err)
	}
	status, err = m.RestartRetirementCleanup(t.Context(), f.id, p.OperationID, before)
	if err != nil || status.Revision != before+1 || status.Phase != "cleanup" {
		t.Fatal(status, err)
	}
	if _, err = m.RestartRetirementCleanup(t.Context(), f.id, p.OperationID, before); !errors.Is(err, ErrConflict) {
		t.Fatal("stale cursor reset accepted", err)
	}
	finishRetirement(t, f, m, status)
}

func TestRetirementCannotGrowRecoverySnapshotBeyondExistingCap(t *testing.T) {
	f := newFixture(t)
	if _, err := f.manager.Execute(t.Context(), f.claim, request("scalar"), func(context.Context, View) (Transition, error) {
		return Transition{Data: json.RawMessage(`0`), Result: json.RawMessage(`{}`)}, nil
	}); err != nil {
		t.Fatal(err)
	}
	base, _, err := f.manager.readManifest(t.Context(), f.id)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.manager.SetStorageLimit(t.Context(), f.claim, base.StorageUsage.TotalBytes()); err != nil {
		t.Fatal(err)
	}
	if err = f.manager.Release(t.Context(), f.claim); err != nil {
		t.Fatal(err)
	}
	p := retirementRequest(t, f)
	if _, err = f.manager.StartRetirement(t.Context(), f.id, p, "operator"); !errors.Is(err, ErrLimit) {
		t.Fatal("retirement exceeded retained cap", err)
	}
	preview, err := f.manager.PreviewRetirement(t.Context(), f.id)
	if err != nil || preview.Retirement != nil || preview.RecoveryRevision != p.Revision {
		t.Fatal("rejected retirement changed authority", preview, err)
	}
}

func TestRetirementCannotPublishOversizedLegacyRecoverySnapshot(t *testing.T) {
	f := newFixture(t)
	if err := f.manager.Release(t.Context(), f.claim); err != nil {
		t.Fatal(err)
	}
	base, etag, err := f.manager.readManifest(t.Context(), f.id)
	if err != nil {
		t.Fatal(err)
	}
	state := snapshot{Schema: 1, ID: f.id, Version: 1, Data: json.RawMessage(`0`), Receipts: map[string]receipt{"legacy": {Fingerprint: digest(nil), Result: json.RawMessage(`""`), Version: 1}}}
	body, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	saved := state.Receipts["legacy"]
	saved.Result = json.RawMessage(`"` + strings.Repeat("x", api.MaxDurableEntitySnapshotBytes-len(body)) + `"`)
	state.Receipts["legacy"] = saved
	body, err = json.Marshal(state)
	if err != nil || len(body) != api.MaxDurableEntitySnapshotBytes {
		t.Fatal(len(body), err)
	}
	base.Version, base.SnapshotKey, base.SnapshotHash = 1, f.id.prefix()+"snapshots/0/"+uuid.NewString()+".json", digest(body)
	base.StorageLimitBytes = 2 << 20
	base.StorageUsage = &StorageUsage{SnapshotBytes: int64(len(body)), ReceiptCount: 1}
	if _, err = f.store.Put(t.Context(), base.SnapshotKey, body, ""); err != nil {
		t.Fatal(err)
	}
	if err = f.manager.putManifest(t.Context(), base, etag); err != nil {
		t.Fatal(err)
	}
	p := retirementRequest(t, f)
	if _, err = f.manager.StartRetirement(t.Context(), f.id, p, "operator"); !errors.Is(err, ErrLimit) {
		t.Fatal("oversized recovery snapshot published", err)
	}
	latest, err := f.manager.PreviewRetirement(t.Context(), f.id)
	if err != nil || latest.Ready || latest.Retirement != nil || latest.RecoveryRevision != p.Revision {
		t.Fatal(latest, err)
	}
}
