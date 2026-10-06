package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestBindingsReleasePolicyCLI(t *testing.T) {
	for _, scenario := range []string{"get", "set", "disable", "missing revision", "missing reason", "wrong scope", "wrong ack", "wrong revision", "wrong age", "invalid timestamp"} {
		t.Run(scenario, func(t *testing.T) {
			resetJSONOut(t)
			setPreviewTestAuth(t)
			calls := 0
			now := time.Now().UTC()
			p := api.BindingReleasePolicy{AppID: "11111111-1111-4111-8111-111111111111", Scope: "production", Mode: "enforce", Revision: 1, MaxVerificationAge: "1m0s", RequireApplicationAck: true, UpdatedAt: &now}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/v1/apps/demo/bindings/release-policy" || r.URL.Query().Get("scope") != "production" {
					t.Errorf("route: %s", r.URL)
				}
				if scenario == "get" {
					p.Mode = "off"
					p.Revision = 0
					p.MaxVerificationAge = "10m0s"
					p.RequireApplicationAck = false
					p.UpdatedAt = nil
					if r.Method != "GET" {
						t.Error("method")
					}
				} else {
					var request api.SetBindingReleasePolicyRequest
					if r.Method != "PUT" || json.NewDecoder(r.Body).Decode(&request) != nil || request.ExpectedRevision == nil || *request.ExpectedRevision != 0 {
						t.Error("request")
					}
					if scenario == "disable" {
						p.Mode = "off"
						p.Reason = "incident recovery"
						p.RequireApplicationAck = false
					}
				}
				switch scenario {
				case "wrong scope":
					p.Scope = "staging"
				case "wrong ack":
					p.RequireApplicationAck = false
				case "wrong revision":
					p.Revision = 4
				case "wrong age":
					p.MaxVerificationAge = "10m"
				case "invalid timestamp":
					p.UpdatedAt = nil
				}
				writeJSONTest(w, p)
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			var out bytes.Buffer
			old := osStdout
			osStdout = &out
			t.Cleanup(func() { osStdout = old })
			args := []string{"bindings", "release-policy", "set", "demo", "--scope", "production", "--require-verification", "--max-age", "1m", "--require-application-ack", "--expected-revision", "0", "--json"}
			if scenario == "get" {
				args = []string{"bindings", "release-policy", "get", "demo", "--scope", "production", "--json"}
			}
			if scenario == "disable" || scenario == "missing reason" {
				args = []string{"bindings", "release-policy", "set", "demo", "--scope", "production", "--mode", "off", "--max-age", "1m", "--expected-revision", "0", "--json"}
				if scenario == "disable" {
					args = append(args, "--reason", "incident recovery")
				}
			}
			if scenario == "missing revision" {
				args = []string{"bindings", "release-policy", "set", "demo", "--require-verification"}
			}
			want := 1
			if scenario == "get" || scenario == "set" || scenario == "disable" {
				want = 0
			}
			if code := run(args); code != want {
				t.Fatalf("exit %d want %d: %s", code, want, out.String())
			}
			if (scenario == "missing revision" || scenario == "missing reason") && calls != 0 {
				t.Fatal("invalid input reached API")
			}
		})
	}
}
