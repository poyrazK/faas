package neon

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestEndpointSettingsFollowTheSuspendTimeoutSetting(t *testing.T) {
	p := &Provider{}
	spec := testDatabaseSpec()
	spec.ScaleToZero = true
	if got := p.endpointSettingsForSpec(spec).SuspendTimeoutSecond; got != managedpostgres.DefaultSuspendTimeoutSeconds {
		t.Fatalf("default timeout = %d, want %d", got, managedpostgres.DefaultSuspendTimeoutSeconds)
	}
	p.SetSuspendTimeout(90)
	if got := p.endpointSettingsForSpec(spec).SuspendTimeoutSecond; got != 90 {
		t.Fatalf("timeout = %d, want 90", got)
	}
	spec.ScaleToZero = false
	if got := p.endpointSettingsForSpec(spec).SuspendTimeoutSecond; got != -1 {
		t.Fatalf("always-on timeout = %d, want -1", got)
	}
	// An endpoint created under an earlier timeout is not drift; flipping
	// between scale-to-zero and always-on is.
	want := endpointSettings{MinimumCU: .25, MaximumCU: 1, SuspendTimeoutSecond: 120}
	if !sameEndpointSettings(endpointSettings{MinimumCU: .25, MaximumCU: 1, SuspendTimeoutSecond: 300}, want) {
		t.Fatal("an older scale-to-zero timeout counted as drift")
	}
	if sameEndpointSettings(endpointSettings{MinimumCU: .25, MaximumCU: 1, SuspendTimeoutSecond: -1}, want) {
		t.Fatal("always-on matched a scale-to-zero spec")
	}
}

func TestConvergeSuspendTimeoutWritesOnlyASuspendedEndpoint(t *testing.T) {
	for _, tc := range []struct {
		name     string
		state    string
		timeout  int64
		disabled bool
		want     managedpostgres.SuspendTimeoutResult
		patched  bool
	}{
		{name: "suspended old timeout", state: "idle", timeout: 300, want: managedpostgres.SuspendTimeoutChanged, patched: true},
		{name: "running", state: "active", timeout: 300, want: managedpostgres.SuspendTimeoutDeferred},
		{name: "held", state: "idle", timeout: 300, disabled: true, want: managedpostgres.SuspendTimeoutDeferred},
		{name: "already set", state: "idle", timeout: 120, want: managedpostgres.SuspendTimeoutConverged},
		{name: "always-on", state: "active", timeout: -1, want: managedpostgres.SuspendTimeoutConverged},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var patched any
			provider := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/projects/quiet-river-12345678":
					writeResponse(t, w, http.StatusOK, map[string]any{"project": map[string]any{"id": "quiet-river-12345678"}})
				case "/api/v2/projects/quiet-river-12345678/branches":
					writeResponse(t, w, http.StatusOK, map[string]any{"branches": []map[string]any{{"id": "br-main-123", "default": true, "current_state": "ready"}}})
				case "/api/v2/projects/quiet-river-12345678/endpoints":
					writeResponse(t, w, http.StatusOK, map[string]any{"endpoints": []map[string]any{{"id": "ep-main-123", "branch_id": "br-main-123", "type": "read_write",
						"current_state": tc.state, "disabled": tc.disabled, "autoscaling_limit_min_cu": 0.25, "autoscaling_limit_max_cu": 1, "suspend_timeout_seconds": tc.timeout}}})
				case "/api/v2/projects/quiet-river-12345678/endpoints/ep-main-123":
					if r.Method != http.MethodPatch {
						t.Errorf("unexpected %s", r.Method)
					}
					var body map[string]map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					mu.Lock()
					patched = body["endpoint"]["suspend_timeout_seconds"]
					mu.Unlock()
					if len(body["endpoint"]) != 1 {
						t.Errorf("patch touched more than the timeout: %v", body)
					}
					writeResponse(t, w, http.StatusOK, map[string]any{})
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					writeResponse(t, w, http.StatusNotFound, nil)
				}
			}))
			got, err := provider.ConvergeSuspendTimeout(context.Background(), "quiet-river-12345678", 120)
			if err != nil || got != tc.want {
				t.Fatalf("ConvergeSuspendTimeout = %v, %v; want %v", got, err, tc.want)
			}
			mu.Lock()
			defer mu.Unlock()
			if tc.patched != (patched != nil) || (tc.patched && patched != float64(120)) {
				t.Fatalf("patched suspend_timeout_seconds = %v, want patched=%v with 120", patched, tc.patched)
			}
		})
	}
}
