// adr: 20261025094915 — compute caps hold on running databases.
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

type computeCapFixture struct {
	mu           sync.Mutex
	used         int64
	computeQuota *int64
	logicalQuota *int64
	// transferQuota and transferred are the egress quota backstop's
	// settings.quota.data_transfer_bytes and the project's counter.
	transferQuota *int64
	transferred   int64
	patches       []map[string]any
	ignorePatch   bool
	periodStart   string
	wrongProject  bool
}

func computeCapProvider(t *testing.T, f *computeCapFixture) *Provider {
	t.Helper()
	if f.periodStart == "" {
		f.periodStart = "2026-10-01T00:00:00Z"
	}
	const path = "/api/v2/projects/quiet-river-12345678"
	return testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.URL.Path != path {
			t.Errorf("unexpected path %s", r.URL.Path)
			writeResponse(t, w, http.StatusNotFound, nil)
			return
		}
		switch r.Method {
		case http.MethodGet:
			id := "quiet-river-12345678"
			if f.wrongProject {
				id = "other-river-12345678"
			}
			quota := map[string]any{}
			if f.computeQuota != nil {
				quota["compute_time_seconds"] = *f.computeQuota
			}
			if f.logicalQuota != nil {
				quota["logical_size_bytes"] = *f.logicalQuota
			}
			if f.transferQuota != nil {
				quota["data_transfer_bytes"] = *f.transferQuota
			}
			writeResponse(t, w, http.StatusOK, map[string]any{"project": map[string]any{
				"id": id, "compute_time_seconds": f.used, "consumption_period_start": f.periodStart, "data_transfer_bytes": f.transferred,
				"settings": map[string]any{"quota": quota},
			}})
		case http.MethodPatch:
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			f.patches = append(f.patches, payload)
			if !f.ignorePatch {
				quota := payload["project"].(map[string]any)["settings"].(map[string]any)["quota"].(map[string]any)
				for key, field := range map[string]**int64{"compute_time_seconds": &f.computeQuota, "logical_size_bytes": &f.logicalQuota, "data_transfer_bytes": &f.transferQuota} {
					if value, ok := quota[key].(float64); ok {
						v := int64(value)
						*field = &v
					}
				}
			}
			writeResponse(t, w, http.StatusOK, map[string]any{"project": map[string]any{"id": "quiet-river-12345678"}})
		default:
			t.Errorf("unexpected method %s", r.Method)
		}
	}))
}

func TestObserveComputeCap(t *testing.T) {
	quota := int64(90000)
	f := &computeCapFixture{used: 4321, computeQuota: &quota}
	provider := computeCapProvider(t, f)
	for _, id := range []string{"quiet-river-12345678", "quiet-river-12345678/br-restore-123"} {
		observed, err := provider.ObserveComputeCap(context.Background(), id)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		want := managedpostgres.ComputeCapObservation{Scope: "quiet-river-12345678", PeriodStart: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), UsedUnitSeconds: 4321, QuotaUnitSeconds: 90000}
		if observed != want {
			t.Fatalf("%s: %+v; want %+v (a restore shares its project's quota)", id, observed, want)
		}
	}
	f.computeQuota = nil
	if observed, err := provider.ObserveComputeCap(context.Background(), "quiet-river-12345678"); err != nil || observed.QuotaUnitSeconds != 0 {
		t.Fatalf("no quota: %+v, %v", observed, err)
	}
	f.periodStart = "not a time"
	if _, err := provider.ObserveComputeCap(context.Background(), "quiet-river-12345678"); !errors.Is(err, managedpostgres.ErrUnavailable) {
		t.Fatalf("bad period start: %v", err)
	}
	f.periodStart, f.wrongProject = "2026-10-01T00:00:00Z", true
	if _, err := provider.ObserveComputeCap(context.Background(), "quiet-river-12345678"); !errors.Is(err, managedpostgres.ErrUnavailable) {
		t.Fatalf("another project's body: %v", err)
	}
}

func TestSetComputeCapPreservesTheStorageLimit(t *testing.T) {
	cases := []struct {
		name    string
		logical *int64
	}{
		{name: "with a storage limit", logical: ptrInt64(10 << 30)},
		{name: "without a storage limit", logical: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &computeCapFixture{used: 100, logicalQuota: tc.logical}
			provider := computeCapProvider(t, f)
			if err := provider.SetComputeCap(context.Background(), "quiet-river-12345678/br-restore-123", 18000); err != nil {
				t.Fatal(err)
			}
			if len(f.patches) != 1 {
				t.Fatalf("patches %v", f.patches)
			}
			quota := f.patches[0]["project"].(map[string]any)["settings"].(map[string]any)["quota"].(map[string]any)
			if quota["compute_time_seconds"] != float64(18000) {
				t.Fatalf("compute quota %v", quota)
			}
			logical, sent := quota["logical_size_bytes"]
			if (tc.logical == nil) == sent || (tc.logical != nil && logical != float64(*tc.logical)) {
				t.Fatalf("storage limit sent as %v; want %v", logical, tc.logical)
			}
			if len(quota) != 1+boolInt(tc.logical != nil) {
				t.Fatalf("patch touched other quotas: %v", quota)
			}
		})
	}
}

func TestSetComputeCapRefusesUnconfirmedAndZeroQuotas(t *testing.T) {
	f := &computeCapFixture{used: 100, ignorePatch: true}
	provider := computeCapProvider(t, f)
	if err := provider.SetComputeCap(context.Background(), "quiet-river-12345678", 18000); !errors.Is(err, managedpostgres.ErrUnavailable) {
		t.Fatalf("unconfirmed patch: %v", err)
	}
	if err := provider.SetComputeCap(context.Background(), "quiet-river-12345678", 0); !errors.Is(err, managedpostgres.ErrInvalid) {
		t.Fatalf("zero quota means unlimited to Neon and must be refused: %v", err)
	}
	if len(f.patches) != 1 {
		t.Fatalf("patches %d; want only the unconfirmed one", len(f.patches))
	}
}

func ptrInt64(v int64) *int64 { return &v }

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// adr: 20261110160000 — slice 4: the egress quota backstop is Neon's
// settings.quota.data_transfer_bytes; a write of one quota keeps the others.
func TestObserveEgressCap(t *testing.T) {
	f := &computeCapFixture{transferred: 7 << 30, transferQuota: ptrInt64(500_000_000_000)}
	provider := computeCapProvider(t, f)
	for _, id := range []string{"quiet-river-12345678", "quiet-river-12345678/br-restore-123"} {
		observed, err := provider.ObserveEgressCap(context.Background(), id)
		want := managedpostgres.EgressCapObservation{Scope: "quiet-river-12345678", PeriodStart: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), UsedBytes: 7 << 30, QuotaBytes: 500_000_000_000}
		if err != nil || observed != want {
			t.Fatalf("%s: %+v, %v; want %+v (a restore shares its project's quota)", id, observed, err, want)
		}
	}
	f.transferQuota = nil
	if observed, err := provider.ObserveEgressCap(context.Background(), "quiet-river-12345678"); err != nil || observed.QuotaBytes != 0 {
		t.Fatalf("no quota: %+v, %v", observed, err)
	}
	f.transferred = -1
	if _, err := provider.ObserveEgressCap(context.Background(), "quiet-river-12345678"); !errors.Is(err, managedpostgres.ErrUnavailable) {
		t.Fatalf("negative counter: %v", err)
	}
}

func TestQuotaWritesKeepEachOther(t *testing.T) {
	f := &computeCapFixture{computeQuota: ptrInt64(36000), logicalQuota: ptrInt64(10 << 30)}
	provider := computeCapProvider(t, f)
	ctx := context.Background()
	if err := provider.SetEgressCap(ctx, "quiet-river-12345678", 500_000_000_000); err != nil {
		t.Fatal(err)
	}
	if err := provider.SetComputeCap(ctx, "quiet-river-12345678", 18000); err != nil {
		t.Fatal(err)
	}
	if err := provider.SetStorageCap(ctx, "quiet-river-12345678", 20<<30); err != nil {
		t.Fatal(err)
	}
	if len(f.patches) != 3 {
		t.Fatalf("patches %v", f.patches)
	}
	want := []map[string]any{
		{"compute_time_seconds": float64(36000), "logical_size_bytes": float64(10 << 30), "data_transfer_bytes": float64(500_000_000_000)},
		{"compute_time_seconds": float64(18000), "logical_size_bytes": float64(10 << 30), "data_transfer_bytes": float64(500_000_000_000)},
		{"compute_time_seconds": float64(18000), "logical_size_bytes": float64(20 << 30), "data_transfer_bytes": float64(500_000_000_000)},
	}
	for i, patch := range f.patches {
		quota := patch["project"].(map[string]any)["settings"].(map[string]any)["quota"].(map[string]any)
		if len(quota) != len(want[i]) {
			t.Fatalf("patch %d = %v; want %v", i, quota, want[i])
		}
		for key, value := range want[i] {
			if quota[key] != value {
				t.Fatalf("patch %d = %v; want %v", i, quota, want[i])
			}
		}
	}
}

func TestSetEgressCapRefusesUnconfirmedAndZeroQuotas(t *testing.T) {
	f := &computeCapFixture{ignorePatch: true}
	provider := computeCapProvider(t, f)
	if err := provider.SetEgressCap(context.Background(), "quiet-river-12345678", 500_000_000_000); !errors.Is(err, managedpostgres.ErrUnavailable) {
		t.Fatalf("unconfirmed patch: %v", err)
	}
	if err := provider.SetEgressCap(context.Background(), "quiet-river-12345678", 0); !errors.Is(err, managedpostgres.ErrInvalid) {
		t.Fatalf("zero quota means unlimited to Neon and must be refused: %v", err)
	}
	if len(f.patches) != 1 {
		t.Fatalf("patches %d; want only the unconfirmed one", len(f.patches))
	}
}
