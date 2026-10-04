// adr: 567
package neon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

type snapshotFixture struct {
	t                   *testing.T
	p                   *Provider
	mu                  sync.Mutex
	request             managedpostgres.SnapshotCaptureRequest
	rows                []snapshot
	posts, patches      int
	deletes             int
	losePost, losePatch bool
	keepExpiry          bool
	deletionPending     bool
	malformedList       bool
	missingExpiry       bool
	changeAfterPatch    bool
}

func newSnapshotFixture(t *testing.T, expiry bool) *snapshotFixture {
	t.Helper()
	f := &snapshotFixture{t: t, rows: []snapshot{}, request: managedpostgres.SnapshotCaptureRequest{
		ResourceID: "clone-operation/database", SourceResourceID: "quiet-river-12345678/br-source-123", IdempotencyKey: "checkpoint",
		PointInTime: time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)}}
	f.p = testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		const projectPath = "/api/v2/projects/quiet-river-12345678"
		switch {
		case r.Method == http.MethodGet && r.URL.Path == projectPath+"/snapshots":
			if f.malformedList {
				writeResponse(t, w, http.StatusOK, map[string]any{})
				return
			}
			if f.missingExpiry && len(f.rows) != 0 {
				data, _ := json.Marshal(f.rows[0])
				var raw map[string]any
				_ = json.Unmarshal(data, &raw)
				delete(raw, "expires_at")
				writeResponse(t, w, http.StatusOK, map[string]any{"snapshots": []any{raw}})
				return
			}
			writeResponse(t, w, http.StatusOK, map[string]any{"snapshots": f.rows})
		case r.Method == http.MethodPost && r.URL.Path == projectPath+"/branches/br-source-123/snapshot":
			f.posts++
			q := r.URL.Query()
			if len(q) != 2 || q.Get("timestamp") != f.request.PointInTime.Format(time.RFC3339Nano) || q.Get("name") != f.p.snapshotName(f.request.ResourceID) || r.ContentLength != 0 {
				t.Error("snapshot creation did not use native query parameters and the exact captured branch/point")
			}
			row := f.observation()
			if expiry {
				row.ExpiresAt, _ = json.Marshal(time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano))
			}
			f.rows = append(f.rows, row)
			if f.losePost {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			writeResponse(t, w, http.StatusOK, map[string]any{"snapshot": row, "operations": []any{map[string]any{"id": "running-operation", "status": "running"}}})
		case r.Method == http.MethodPatch && r.URL.Path == projectPath+"/snapshots/snap-captured-123":
			f.patches++
			var payload map[string]map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || len(payload) != 1 || len(payload["snapshot"]) != 1 || string(payload["snapshot"]["expires_at"]) != "null" {
				t.Error("retention did not explicitly clear only snapshot expiry")
			}
			if !f.keepExpiry {
				f.rows[0].ExpiresAt = json.RawMessage("null")
			}
			if f.changeAfterPatch {
				f.rows[0].Timestamp = f.request.PointInTime.Add(time.Second).Format(time.RFC3339Nano)
			}
			if f.losePatch {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			// Deliberately return no expiry evidence in the acknowledgement.
			writeResponse(t, w, http.StatusOK, map[string]any{})
		case r.Method == http.MethodDelete && r.URL.Path == projectPath+"/snapshots/snap-captured-123":
			f.deletes++
			if !f.deletionPending {
				f.rows = []snapshot{}
			}
			writeResponse(t, w, http.StatusAccepted, map[string]any{"operations": []any{map[string]any{"id": "deleting-operation", "status": "running"}}})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	return f
}

func (f *snapshotFixture) observation() snapshot {
	return snapshot{ID: "snap-captured-123", Name: f.p.snapshotName(f.request.ResourceID), SourceBranchID: "br-source-123",
		Timestamp: f.request.PointInTime.Format(time.RFC3339Nano), CreatedAt: f.request.PointInTime.Add(time.Minute).Format(time.RFC3339Nano),
		ExpiresAt: json.RawMessage("null"), Manual: true}
}

func TestSnapshotCaptureRetainsExactDataAndRecoversAcknowledgements(t *testing.T) {
	for _, mode := range []string{"no_expiry", "clear_expiry", "lost_post", "lost_patch"} {
		t.Run(mode, func(t *testing.T) {
			f := newSnapshotFixture(t, mode != "no_expiry")
			f.losePost, f.losePatch = mode == "lost_post", mode == "lost_patch"
			captured, err := f.p.CaptureSnapshot(t.Context(), f.request)
			if err != nil || captured.ProviderSnapshotID != "quiet-river-12345678/snapshots/snap-captured-123" ||
				captured.SourceResourceID != f.request.SourceResourceID || !captured.PointInTime.Equal(f.request.PointInTime) || captured.CreatedAt.IsZero() || captured.ExpiresAt != nil {
				t.Fatalf("snapshot capture: %+v, %v", captured, err)
			}
			replayed, err := f.p.CaptureSnapshot(t.Context(), f.request)
			if err != nil || replayed.ProviderSnapshotID != captured.ProviderSnapshotID || !replayed.PointInTime.Equal(captured.PointInTime) {
				t.Fatalf("snapshot replay: %+v, %v", replayed, err)
			}
			observed, err := f.p.InspectSnapshot(t.Context(), captured.ProviderSnapshotID)
			if err != nil || observed.ProviderSnapshotID != captured.ProviderSnapshotID || observed.ExpiresAt != nil {
				t.Fatalf("snapshot observation: %+v, %v", observed, err)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			wantPatch := 1
			if mode == "no_expiry" {
				wantPatch = 0
			}
			if f.posts != 1 || f.patches != wantPatch || f.deletes != 0 {
				t.Fatalf("replay repeated provider mutation: POST=%d PATCH=%d DELETE=%d", f.posts, f.patches, f.deletes)
			}
		})
	}
}

func TestSnapshotCaptureRejectsUnverifiedIdentityAndRetention(t *testing.T) {
	for _, fault := range []string{"wrong_source", "wrong_point", "scheduled_snapshot", "missing_expiry", "invalid_expiry", "duplicate_name", "malformed_list", "retention_not_applied", "identity_changed_after_patch"} {
		t.Run(fault, func(t *testing.T) {
			f := newSnapshotFixture(t, false)
			f.rows = []snapshot{f.observation()}
			want := managedpostgres.ErrUnavailable
			switch fault {
			case "wrong_source":
				f.rows[0].SourceBranchID, want = "br-other-123", managedpostgres.ErrConflict
			case "wrong_point":
				f.rows[0].Timestamp, want = f.request.PointInTime.Add(time.Second).Format(time.RFC3339Nano), managedpostgres.ErrConflict
			case "scheduled_snapshot":
				f.rows[0].Manual = false
			case "missing_expiry":
				f.missingExpiry = true
			case "invalid_expiry":
				f.rows[0].ExpiresAt = json.RawMessage(`"unknown"`)
			case "duplicate_name":
				f.rows = append(f.rows, f.rows[0])
				f.rows[1].ID, want = "snap-duplicate-123", managedpostgres.ErrConflict
			case "malformed_list":
				f.malformedList = true
			case "retention_not_applied", "identity_changed_after_patch":
				f.rows[0].ExpiresAt, _ = json.Marshal(time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano))
				f.keepExpiry, f.changeAfterPatch = fault == "retention_not_applied", fault == "identity_changed_after_patch"
				if f.changeAfterPatch {
					want = managedpostgres.ErrConflict
				}
			}
			if _, err := f.p.CaptureSnapshot(t.Context(), f.request); !errors.Is(err, want) {
				t.Fatalf("unverified snapshot capture accepted: %v", err)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.posts != 0 || f.deletes != 0 || fault != "retention_not_applied" && fault != "identity_changed_after_patch" && f.patches != 0 {
				t.Fatal("invalid capture recreated a snapshot or changed unrelated data")
			}
		})
	}
}

func TestSnapshotCaptureRequiresPinnedSourceAndPreservesPoint(t *testing.T) {
	for _, fault := range []string{"default_selector", "zero_point", "future_point", "submicrosecond_point", "empty_owner", "empty_key", "cancelled"} {
		t.Run(fault, func(t *testing.T) {
			f := newSnapshotFixture(t, false)
			r, ctx, want := f.request, t.Context(), managedpostgres.ErrInvalid
			switch fault {
			case "default_selector":
				r.SourceResourceID = "quiet-river-12345678"
			case "zero_point":
				r.PointInTime = time.Time{}
			case "future_point":
				r.PointInTime = time.Now().Add(time.Hour).Truncate(time.Microsecond)
			case "submicrosecond_point":
				r.PointInTime = r.PointInTime.Add(time.Nanosecond)
			case "empty_owner":
				r.ResourceID = ""
			case "empty_key":
				r.IdempotencyKey = ""
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				want = context.Canceled
			}
			if _, err := f.p.CaptureSnapshot(ctx, r); !errors.Is(err, want) {
				t.Fatalf("invalid checkpoint accepted: %v", err)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.posts != 0 || f.patches != 0 || f.deletes != 0 {
				t.Fatal("invalid checkpoint mutated provider resources")
			}
		})
	}
}

func TestSnapshotCleanupRequiresOwnedObservedIdentityAndAbsence(t *testing.T) {
	for _, mode := range []string{"deleted", "pending", "foreign_owner", "wrong_point", "wrong_source", "foreign_project", "database_selector"} {
		t.Run(mode, func(t *testing.T) {
			f := newSnapshotFixture(t, false)
			f.rows = []snapshot{f.observation()}
			r := managedpostgres.SnapshotDeleteRequest{ResourceID: f.request.ResourceID, SourceResourceID: f.request.SourceResourceID,
				PointInTime: f.request.PointInTime, ProviderSnapshotID: "quiet-river-12345678/snapshots/snap-captured-123"}
			var want error
			switch mode {
			case "pending":
				f.deletionPending = true
			case "foreign_owner":
				r.ResourceID, want = "foreign-operation", managedpostgres.ErrConflict
			case "wrong_point":
				r.PointInTime, want = r.PointInTime.Add(time.Second), managedpostgres.ErrConflict
			case "wrong_source":
				r.SourceResourceID, want = "quiet-river-12345678/br-other-123", managedpostgres.ErrConflict
			case "foreign_project":
				r.ProviderSnapshotID, want = "other-project/snapshots/snap-captured-123", managedpostgres.ErrInvalid
			case "database_selector":
				r.ProviderSnapshotID, want = r.SourceResourceID, managedpostgres.ErrInvalid
			}
			result, err := f.p.DeleteSnapshot(t.Context(), r)
			if !errors.Is(err, want) || result.Done != (mode == "deleted") {
				t.Fatalf("snapshot cleanup: %+v, %v", result, err)
			}
			if mode == "deleted" {
				if replay, err := f.p.DeleteSnapshot(t.Context(), r); err != nil || !replay.Done {
					t.Fatalf("cleanup replay: %+v, %v", replay, err)
				}
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			wantDelete := 0
			if mode == "deleted" || mode == "pending" {
				wantDelete = 1
			}
			if f.deletes != wantDelete || f.posts != 0 || f.patches != 0 {
				t.Fatal("cleanup deleted a foreign snapshot or changed source resources")
			}
		})
	}
}

func TestSnapshotDiscoveryNeverCreatesOrChangesRetention(t *testing.T) {
	f := newSnapshotFixture(t, true)
	if _, err := f.p.FindSnapshot(t.Context(), f.request); !errors.Is(err, managedpostgres.ErrNotFound) {
		t.Fatalf("absent intent = %v", err)
	}
	row := f.observation()
	expiry := time.Now().UTC().Add(time.Hour)
	row.ExpiresAt, _ = json.Marshal(expiry.Format(time.RFC3339Nano))
	f.rows = append(f.rows, row)
	actual, err := f.p.FindSnapshot(t.Context(), f.request)
	if err != nil || actual.ExpiresAt == nil || !actual.ExpiresAt.Equal(expiry) {
		t.Fatalf("find actual retention = %+v, %v", actual, err)
	}
	f.rows[0].Name = "foreign-owner"
	if _, err := f.p.FindSnapshot(t.Context(), f.request); !errors.Is(err, managedpostgres.ErrNotFound) {
		t.Fatalf("foreign name = %v", err)
	}
	// Missing the name is not missing the known ID. Cleanup must still reject
	// the changed owner and preserve its data, rather than report completion.
	if _, err := f.p.DeleteSnapshot(t.Context(), managedpostgres.SnapshotDeleteRequest{ResourceID: f.request.ResourceID,
		ProviderSnapshotID: actual.ProviderSnapshotID, SourceResourceID: f.request.SourceResourceID, PointInTime: f.request.PointInTime}); !errors.Is(err, managedpostgres.ErrConflict) {
		t.Fatalf("known foreign identity cleanup = %v", err)
	}
	if f.posts != 0 || f.patches != 0 || f.deletes != 0 {
		t.Fatal("discovery modified provider")
	}
}

func TestSnapshotRetentionCannotCreateReplacement(t *testing.T) {
	f := newSnapshotFixture(t, true)
	id := "quiet-river-12345678/snapshots/snap-captured-123"
	if _, err := f.p.RetainSnapshot(t.Context(), f.request, id); !errors.Is(err, managedpostgres.ErrNotFound) {
		t.Fatalf("missing copy = %v", err)
	}
	if f.posts != 0 || f.patches != 0 {
		t.Fatal("missing snapshot replaced")
	}
	row := f.observation()
	row.ExpiresAt, _ = json.Marshal(time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano))
	f.rows = append(f.rows, row)
	actual, err := f.p.RetainSnapshot(t.Context(), f.request, id)
	if err != nil || actual.ExpiresAt != nil || f.posts != 0 || f.patches != 1 {
		t.Fatalf("retained actual copy = %+v, %v", actual, err)
	}
	f.rows[0].SourceBranchID = "br-other"
	if _, err := f.p.RetainSnapshot(t.Context(), f.request, id); !errors.Is(err, managedpostgres.ErrConflict) {
		t.Fatalf("foreign snapshot = %v", err)
	}
	if f.posts != 0 || f.patches != 1 {
		t.Fatal("changed identity mutated retention")
	}
}
