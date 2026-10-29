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
