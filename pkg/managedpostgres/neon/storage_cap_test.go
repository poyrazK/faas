// adr: 20261025094915 — the storage pool limits each branch of a project
// (decision 9).
package neon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

type storageCapFixture struct {
	mu             sync.Mutex
	computeQuota   *int64
	logicalQuota   *int64
	branches       []map[string]any
	patches        []map[string]any
	ignorePatch    bool
	unknownBranch  bool
	branchRequests int
}

func storageCapProvider(t *testing.T, f *storageCapFixture) *Provider {
	t.Helper()
	const project = "/api/v2/projects/quiet-river-12345678"
	return testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.URL.Path == project && r.Method == http.MethodGet:
			quota := map[string]any{}
			if f.computeQuota != nil {
				quota["compute_time_seconds"] = *f.computeQuota
			}
			if f.logicalQuota != nil {
				quota["logical_size_bytes"] = *f.logicalQuota
			}
			writeResponse(t, w, http.StatusOK, map[string]any{"project": map[string]any{
				"id": "quiet-river-12345678", "consumption_period_start": "2026-10-01T00:00:00Z", "settings": map[string]any{"quota": quota},
			}})
		case r.URL.Path == project && r.Method == http.MethodPatch:
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			f.patches = append(f.patches, payload)
			if !f.ignorePatch {
				quota := payload["project"].(map[string]any)["settings"].(map[string]any)["quota"].(map[string]any)
				if value, ok := quota["logical_size_bytes"].(float64); ok {
					v := int64(value)
					f.logicalQuota = &v
				}
			}
			writeResponse(t, w, http.StatusOK, map[string]any{"project": map[string]any{"id": "quiet-river-12345678"}})
		case r.URL.Path == project+"/branches":
			f.branchRequests++
			writeResponse(t, w, http.StatusOK, map[string]any{"branches": f.branches})
		case r.URL.Path == project+"/branches/br-restore-123":
			f.branchRequests++
			id := "br-restore-123"
			if f.unknownBranch {
				id = "br-other-123"
			}
			writeResponse(t, w, http.StatusOK, map[string]any{"branch": map[string]any{"id": id, "logical_size": 7 << 20}})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			writeResponse(t, w, http.StatusNotFound, nil)
		}
	}))
}

func TestObserveStorageCap(t *testing.T) {
	limit := int64(512 << 20)
	f := &storageCapFixture{logicalQuota: &limit, branches: []map[string]any{
		{"id": "br-main-123", "default": true, "logical_size": 300 << 20},
		{"id": "br-restore-123", "default": false, "logical_size": 7 << 20},
	}}
	provider := storageCapProvider(t, f)
	for id, want := range map[string]managedpostgres.StorageCapObservation{
		"quiet-river-12345678":                {Scope: "quiet-river-12345678", SizeBytes: 300 << 20, LimitBytes: 512 << 20},
		"quiet-river-12345678/br-restore-123": {Scope: "quiet-river-12345678", SizeBytes: 7 << 20, LimitBytes: 512 << 20},
	} {
		observed, err := provider.ObserveStorageCap(context.Background(), id)
		if err != nil || observed != want {
			t.Fatalf("%s: %+v, %v; want %+v (a restore shares its project's limit)", id, observed, err, want)
		}
	}
	f.logicalQuota = nil
	if observed, err := provider.ObserveStorageCap(context.Background(), "quiet-river-12345678"); err != nil || observed.LimitBytes != 0 {
		t.Fatalf("no limit: %+v, %v", observed, err)
	}
	for name, change := range map[string]func(){
		"no size yet":          func() { delete(f.branches[0], "logical_size") },
		"no default branch":    func() { f.branches[0]["default"] = false },
		"two default branches": func() { f.branches[0]["default"], f.branches[1]["default"] = true, true },
	} {
		f.branches = []map[string]any{{"id": "br-main-123", "default": true, "logical_size": 1}, {"id": "br-restore-123", "default": false, "logical_size": 1}}
		change()
		if _, err := provider.ObserveStorageCap(context.Background(), "quiet-river-12345678"); err == nil {
			t.Errorf("%s: observed", name)
		}
	}
	f.unknownBranch = true
	if _, err := provider.ObserveStorageCap(context.Background(), "quiet-river-12345678/br-restore-123"); !errors.Is(err, managedpostgres.ErrUnavailable) {
		t.Fatalf("another branch's body: %v", err)
	}
}

func TestSetStorageCapPreservesTheComputeQuota(t *testing.T) {
	for _, compute := range []*int64{ptrInt64(18000), nil} {
		f := &storageCapFixture{computeQuota: compute}
		provider := storageCapProvider(t, f)
		if err := provider.SetStorageCap(context.Background(), "quiet-river-12345678/br-restore-123", 424<<20); err != nil {
			t.Fatal(err)
		}
		if len(f.patches) != 1 {
			t.Fatalf("patches %v", f.patches)
		}
		quota := f.patches[0]["project"].(map[string]any)["settings"].(map[string]any)["quota"].(map[string]any)
		if quota["logical_size_bytes"] != float64(424<<20) {
			t.Fatalf("storage limit %v", quota)
		}
		sent, ok := quota["compute_time_seconds"]
		if (compute == nil) == ok || (compute != nil && sent != float64(*compute)) || len(quota) != 1+boolInt(compute != nil) {
			t.Fatalf("patch %v changed the compute quota %v", quota, compute)
		}
	}
	f := &storageCapFixture{ignorePatch: true}
	provider := storageCapProvider(t, f)
	if err := provider.SetStorageCap(context.Background(), "quiet-river-12345678", 424<<20); !errors.Is(err, managedpostgres.ErrUnavailable) {
		t.Fatalf("an unconfirmed write: %v", err)
	}
	if err := provider.SetStorageCap(context.Background(), "quiet-river-12345678", 0); !errors.Is(err, managedpostgres.ErrInvalid) || len(f.patches) != 1 {
		t.Fatalf("zero is no limit to Neon and must not be sent: %v", err)
	}
}

// Only the server's size-limit error proves a write was refused at the limit;
// a refused connection proves nothing.
func TestStorageLimitRefusal(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want bool
	}{
		"disk full":         {err: &pgconn.PgError{Code: "53100", Message: "could not extend file"}, want: true},
		"size limit text":   {err: &pgconn.PgError{Code: "XX000", Message: "could not extend file because project size limit (512 MB) has been exceeded"}, want: true},
		"another sql error": {err: &pgconn.PgError{Code: "42P01", Message: "relation does not exist"}},
		"connection":        {err: errors.New("dial tcp: connection refused")},
		"success":           {},
	} {
		if got := storageLimitRefusal(tc.err); got != tc.want {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
		}
	}
}

// Neon publishes a branch's logical size only after its compute suspends:
// on 2026-11-22 a 2 MB write was still unreported 20 minutes later on a
// compute that stayed active, and sizes appeared 76 s and 287 s after a
// compute went idle. The probe suspends the compute and waits well past that.
func TestStorageCapProbeWaitsOutNeonsSizeLag(t *testing.T) {
	const measuredLag = 287 * time.Second
	if storageCapProbeSizeWait < 2*measuredLag {
		t.Fatalf("storageCapProbeSizeWait = %v, want at least twice the measured %v", storageCapProbeSizeWait, measuredLag)
	}
}

type suspendFixture struct {
	mu        sync.Mutex
	state     string
	busy      int
	suspends  int
	failCalls bool
}

func suspendProvider(t *testing.T, f *suspendFixture) *Provider {
	t.Helper()
	const project = "/api/v2/projects/quiet-river-12345678"
	return testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.URL.Path == project && r.Method == http.MethodGet:
			writeResponse(t, w, http.StatusOK, map[string]any{"project": map[string]any{"id": "quiet-river-12345678"}})
		case r.URL.Path == project+"/branches":
			writeResponse(t, w, http.StatusOK, map[string]any{"branches": []map[string]any{
				{"id": "br-main-123", "default": true, "current_state": "ready"},
				{"id": "br-restore-123", "default": false, "current_state": "ready"},
			}})
		case r.URL.Path == project+"/endpoints":
			writeResponse(t, w, http.StatusOK, map[string]any{"endpoints": []map[string]any{
				{"id": "ep-main-123", "branch_id": "br-main-123", "type": "read_write", "current_state": f.state},
				{"id": "ep-restore-123", "branch_id": "br-restore-123", "type": "read_write", "current_state": "active"},
			}})
		case r.URL.Path == project+"/endpoints/ep-main-123/suspend" && r.Method == http.MethodPost:
			f.suspends++
			if f.busy > 0 {
				f.busy--
				writeResponse(t, w, http.StatusLocked, map[string]any{"code": "", "message": "project already has running conflicting operations, scheduling of new ones is prohibited"})
				return
			}
			if f.failCalls {
				writeResponse(t, w, http.StatusServiceUnavailable, nil)
				return
			}
			f.state = "idle"
			writeResponse(t, w, http.StatusOK, map[string]any{"endpoint": map[string]any{"id": "ep-main-123", "current_state": "idle"}})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			writeResponse(t, w, http.StatusNotFound, nil)
		}
	}))
}

// The probe suspends the database's own compute, never another branch's,
// leaves an idle one alone, and waits out a busy project.
func TestSuspendForSizeReport(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		state     string
		busy      int
		failCalls bool
		want      error
		suspends  int
	}{
		"active compute":      {state: "active", suspends: 1},
		"idle compute":        {state: "idle", suspends: 0},
		"busy project":        {state: "active", busy: 2, suspends: 3},
		"suspend refused":     {state: "active", failCalls: true, want: managedpostgres.ErrUnavailable, suspends: 1},
		"busy past the bound": {state: "active", busy: endpointHoldBusyAttempts + 1, want: managedpostgres.ErrUnavailable, suspends: endpointHoldBusyAttempts},
	} {
		f := &suspendFixture{state: tc.state, busy: tc.busy, failCalls: tc.failCalls}
		err := suspendProvider(t, f).suspendForSizeReport(ctx, "quiet-river-12345678/br-main-123")
		if !errors.Is(err, tc.want) || (tc.want == nil && err != nil) {
			t.Errorf("%s: err = %v, want %v", name, err, tc.want)
		}
		f.mu.Lock()
		suspends := f.suspends
		f.mu.Unlock()
		if suspends != tc.suspends {
			t.Errorf("%s: %d suspend requests, want %d", name, suspends, tc.suspends)
		}
	}
}
