package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCanaryRouteGateCLI(t *testing.T) {
	for _, scenario := range []string{"get", "set", "mismatched mode", "invalid revision", "invalid mode", "missing timestamp", "missing expected revision"} {
		t.Run(scenario, func(t *testing.T) {
			resetJSONOut(t)
			setPreviewTestAuth(t)
			now := time.Now().UTC()
			gate := api.CanaryRouteGate{AppID: "11111111-1111-4111-8111-111111111111", Mode: "enforce", Revision: 1, UpdatedAt: &now}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/v1/apps/demo/route-requirements/gate" {
					t.Errorf("path: %s", r.URL.Path)
				}
				if scenario == "get" {
					if r.Method != "GET" {
						t.Error("get method")
					}
					gate.Mode, gate.Revision, gate.UpdatedAt = "report", 0, nil
				} else {
					var request api.SetCanaryRouteGateRequest
					if r.Method != "PUT" || json.NewDecoder(r.Body).Decode(&request) != nil || request.ExpectedRevision == nil || *request.ExpectedRevision != 0 || request.Mode != "enforce" {
						t.Error("update binding")
					}
				}
				switch scenario {
				case "mismatched mode":
					gate.Mode = "report"
				case "invalid revision":
					gate.Revision = 4
				case "invalid mode":
					gate.Mode = "auto"
				case "missing timestamp":
					gate.UpdatedAt = nil
				}
				writeJSONTest(w, gate)
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			var out bytes.Buffer
			oldOut := osStdout
			osStdout = &out
			t.Cleanup(func() { osStdout = oldOut })
			args := []string{"routes", "gate", "set", "demo", "--mode", "enforce", "--expected-revision", "0", "--json"}
			if scenario == "get" {
				args = []string{"routes", "gate", "get", "demo", "--json"}
			}
			if scenario == "missing expected revision" {
				args = []string{"routes", "gate", "set", "demo", "--mode", "enforce"}
			}
			want := 1
			if scenario == "get" || scenario == "set" {
				want = 0
			}
			if code := run(args); code != want {
				t.Fatalf("exit %d want %d: %s", code, want, out.String())
			}
			if want == 0 && !strings.Contains(out.String(), `"mode"`) {
				t.Fatal("missing JSON result")
			}
			if scenario == "missing expected revision" && calls != 0 {
				t.Fatal("invalid update reached API")
			}
		})
	}
}
