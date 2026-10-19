// adr: 1623
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCompareAppDebugDeploymentsMetadata(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "modern", true: "legacy"}[legacy], func(t *testing.T) {
			b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req DebugCompareRequest
				if r.Method != "POST" || r.URL.Path != "/v1/apps/alpha/debug/compare" || json.NewDecoder(r.Body).Decode(&req) != nil || req.Source != "source" || req.Mirror != "target" || req.Route != "/orders" || req.Since != "1h" {
					t.Error("SDK changed comparison selection")
				}
				out := map[string]any{"source": "source", "mirror": "target", "routes": []DebugCompareRouteStats{}}
				if !legacy {
					out["app_id"], out["since"], out["window_start"], out["window_end"], out["retention_clamped"], out["route_filter"] = "app-1", "1h0m0s", "2026-10-16T10:00:00Z", "2026-10-16T11:00:00Z", false, "/orders"
				}
				_ = json.NewEncoder(w).Encode(out)
			}))
			defer b.Close()
			out, err := NewClient(b.URL, "").CompareAppDebugDeployments(t.Context(), "alpha", "source", "target", "/orders", "1h", "2026-10-16T11:00:00Z")
			if err != nil {
				t.Fatal(err)
			}
			if legacy {
				if out.RetentionClamped != nil {
					t.Fatal("legacy server fabricated false clamp")
				}
			} else if out.AppID != "app-1" || out.RetentionClamped == nil || *out.RetentionClamped || out.WindowEnd == "" || out.RouteFilter != "/orders" {
				t.Fatalf("metadata lost: %+v", out)
			}
		})
	}
}
