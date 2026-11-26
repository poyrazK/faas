package neon

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// The compute-cap probe measures against the range the database's endpoint
// runs at: a production endpoint created at 1 CU before ADR-20261124221402
// is measured at 1 CU, not at the 0.5 CU floor new ones get.
func TestProbeProfileUsesTheEndpointsRange(t *testing.T) {
	endpoint := func(minimum, maximum float64) map[string]any {
		return map[string]any{"id": "ep-main-123", "branch_id": "br-main-123", "type": "read_write",
			"autoscaling_limit_min_cu": minimum, "autoscaling_limit_max_cu": maximum}
	}
	for _, tc := range []struct {
		name      string
		class     managedpostgres.ServiceClass
		endpoints []map[string]any
		want      classProfile
		wantErr   error
	}{
		{name: "production at 0.5 CU", class: managedpostgres.ClassProduction,
			endpoints: []map[string]any{endpoint(0.5, 4)}, want: classProfile{minimumCU: 0.5, maximumCU: 4}},
		{name: "production created at 1 CU", class: managedpostgres.ClassProduction,
			endpoints: []map[string]any{endpoint(1, 4)}, want: classProfile{minimumCU: 1, maximumCU: 4}},
		{name: "no range reported keeps the class profile", class: managedpostgres.ClassProduction,
			endpoints: []map[string]any{endpoint(0, 0)}, want: classProfile{minimumCU: 0.5, maximumCU: 4}},
		{name: "a restore's second compute keeps the class profile", class: managedpostgres.ClassProduction,
			endpoints: []map[string]any{endpoint(1, 4), {"id": "ep-other-123", "branch_id": "br-other-123", "type": "read_write"}},
			want:      classProfile{minimumCU: 0.5, maximumCU: 4}},
		{name: "burstable", class: managedpostgres.ClassBurstable,
			endpoints: []map[string]any{endpoint(0.25, 2)}, want: classProfile{minimumCU: 0.25, maximumCU: 2}},
		{name: "unknown class", class: "", endpoints: []map[string]any{endpoint(1, 2)}, wantErr: managedpostgres.ErrUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/api/v2/projects/quiet-river-12345678/endpoints" {
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				writeResponse(t, w, http.StatusOK, map[string]any{"endpoints": tc.endpoints})
			}))
			ref, err := parseResourceRef("quiet-river-12345678")
			if err != nil {
				t.Fatal(err)
			}
			got, err := provider.probeProfile(context.Background(), ref, tc.class)
			if !errors.Is(err, tc.wantErr) || tc.wantErr == nil && err != nil {
				t.Fatalf("probeProfile = %v, want %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("probeProfile = %+v, want %+v", got, tc.want)
			}
		})
	}
}
