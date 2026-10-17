// adr: 570
package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdAppTrafficStatusReportsObservedWiring(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		t.Run(map[bool]string{false: "text", true: "json"}[jsonMode], func(t *testing.T) {
			resetJSONOut(t)
			status := api.RuntimePolicyStatusResponse{AppID: "app", State: "active", DesiredRevision: 9,
				TrafficRuntime: api.TrafficRuntimeStatus{Scope: "compute_gateway_wiring", State: "observed", EnforcementStatus: "unverified", ServingGateways: 2, FreshGateways: 2,
					PublicRetry: api.TrafficRuntimeFeatureStatus{State: "mixed", Mode: "mixed"}, RateCounter: api.TrafficRuntimeFeatureStatus{State: "observed", Mode: "local"}}}
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/hello/policy/status" || r.URL.RawQuery != "" {
					t.Errorf("request = %s %s", r.Method, r.URL.String())
				}
				writeJSONTest(w, status)
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_test_x")
			var out bytes.Buffer
			oldOut := osStdout
			osStdout = &out
			t.Cleanup(func() { osStdout = oldOut })
			args := []string{"app", "hello", "traffic-status"}
			if jsonMode {
				args = append(args, "--json")
			}
			if code := run(args); code != 0 || calls != 1 {
				t.Fatalf("code=%d calls=%d output=%s", code, calls, out.String())
			}
			if jsonMode {
				var got api.RuntimePolicyStatusResponse
				if json.Unmarshal(out.Bytes(), &got) != nil || got.TrafficRuntime != status.TrafficRuntime {
					t.Fatalf("JSON observations = %s", out.String())
				}
			} else {
				for _, want := range []string{"fresh 2/2", "Public edge retry: mixed", "Rate counter: local", "Request enforcement: unverified"} {
					if !strings.Contains(out.String(), want) {
						t.Errorf("missing %q in %s", want, out.String())
					}
				}
			}
		})
	}
}

func TestRenderAppTrafficStatusHandlesOlderAPI(t *testing.T) {
	var out bytes.Buffer
	renderAppTrafficStatus(&out, api.RuntimePolicyStatusResponse{State: "active", ServingGateways: 2})
	for _, want := range []string{"Compute gateway wiring: unverified", "missing 2", "Public edge retry: unverified", "Request enforcement: unverified"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("older API missing %q in %s", want, out.String())
		}
	}
}
