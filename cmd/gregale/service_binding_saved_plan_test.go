package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestServiceBindingSavedPlanCLI(t *testing.T) {
	for _, scenario := range []string{"apply", "stale", "race after read", "wrong account", "wrong app id", "modified", "old server"} {
		t.Run(scenario, func(t *testing.T) {
			main.ResetJSONOut(t)
			current := api.AppResponse{ID: "app1", Slug: "hello", ServiceBindingFingerprint: strings.Repeat("a", 64)}
			account := "account1"
			patches := 0
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				switch {
				case r.URL.Path == "/v1/account":
					main.WriteJSONTest(w, api.AccountResponse{ID: account})
				case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/hello":
					main.WriteJSONTest(w, current)
				case r.Method == http.MethodPatch && r.URL.Path == "/v1/apps/hello":
					patches++
					var req api.UpdateAppRequest
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Error(err)
					}
					if req.ExpectedServiceBindingFingerprint == nil || *req.ExpectedServiceBindingFingerprint != strings.Repeat("a", 64) {
						t.Errorf("missing plan precondition: %+v", req)
					}
					if scenario == "race after read" {
						w.WriteHeader(http.StatusConflict)
						main.WriteJSONTest(w, api.Problem{Title: "Service binding plan is stale", Status: http.StatusConflict})
						return
					}
					if req.ServiceBindingTransport == nil || *req.ServiceBindingTransport != api.ServiceBindingTransportHTTPS {
						t.Errorf("patch=%+v", req)
					}
					current.ServiceBindingTransport = api.ServiceBindingTransportHTTPS
					current.ServiceBindingFingerprint = strings.Repeat("b", 64)
					main.WriteJSONTest(w, current)
				default:
					http.Error(w, "unexpected", 500)
				}
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", "fp_test_x")
			var output bytes.Buffer
			old := (*main.OsStdout)
			(*main.OsStdout) = &output
			t.Cleanup(func() { (*main.OsStdout) = old })
			planPath := filepath.Join(t.TempDir(), "plan.json")
			args := []string{"bindings", "service", "configure", "hello", "--file", servicePolicyFile(t, `{"service_binding_transport":"https"}`), "--plan-out", planPath, "--json"}
			if scenario == "old server" {
				current.ServiceBindingFingerprint = ""
			}
			code := main.Run(args)
			if scenario == "old server" {
				if code == 0 || patches != 0 {
					t.Fatal("old server accepted plan")
				}
				return
			}
			if code != 0 || patches != 0 {
				t.Fatalf("plan code=%d patches=%d output=%s", code, patches, output.String())
			}
			info, err := os.Stat(planPath)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0600 {
				t.Fatalf("plan mode=%v", info.Mode())
			}
			if main.Run(args) == 0 {
				t.Fatal("overwrote an existing plan")
			}
			switch scenario {
			case "stale":
				current.ServiceBindingFingerprint = strings.Repeat("b", 64)
			case "wrong account":
				account = "account2"
			case "wrong app id":
				current.ID = "recreated-app"
			case "modified":
				raw, err := os.ReadFile(planPath)
				if err != nil {
					t.Fatal(err)
				}
				raw = bytes.Replace(raw, []byte(`"https"`), []byte(`"http"`), 1)
				if err := os.WriteFile(planPath, raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			beforeApplyCalls := calls
			output.Reset()
			code = main.Run([]string{"bindings", "service", "apply", "hello", "--plan", planPath, "--json"})
			if scenario == "apply" {
				if code != 0 || patches != 1 {
					t.Fatalf("apply code=%d patches=%d", code, patches)
				}
				return
			}
			if code == 0 {
				t.Fatalf("accepted %s", scenario)
			}
			wantPatches := 0
			if scenario == "race after read" {
				wantPatches = 1
			}
			if patches != wantPatches {
				t.Fatalf("scenario=%s patches=%d", scenario, patches)
			}
			if scenario == "modified" && calls != beforeApplyCalls {
				t.Fatal("modified plan made requests")
			}
		})
	}
}
