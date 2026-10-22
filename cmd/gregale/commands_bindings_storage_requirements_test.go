package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestBindingsStorageRequirementsCLIReadOnlyCheck(t *testing.T) {
	for _, mode := range []string{"", "write", "read_write"} {
		t.Run(mode, func(t *testing.T) {
			inventory := main.BindingCheckCLIInventory()
			item := &inventory.Bindings[0]
			item.Access = "read_write"
			item.Verification.ObjectStorageMode = mode
			if mode != "" {
				item.Verification.Checks = append(item.Verification.Checks, api.BindingVerificationCheck{Name: "upload", Status: "passed"}, api.BindingVerificationCheck{Name: "cleanup", Status: "passed"})
			}
			if mode == "read_write" {
				item.Verification.Checks = append(item.Verification.Checks, api.BindingVerificationCheck{Name: "download", Status: "passed"})
			}
			code, out, _ := main.RunBindingCheckCLI(t, inventory, "", "bindings", "check", "api", "--object-storage-require", "ASSETS=read_write", "--json")
			report := main.DecodeBindingCheckCLI(t, out)
			if (code == 0) != (mode == "read_write") || report.ObjectStorageRequirements["ASSETS"] != "read_write" {
				t.Fatalf("wrong operations qualified: code=%d %s", code, out)
			}
		})
	}
}

func TestBindingsStorageRequirementsCLIPolicyConfirmation(t *testing.T) {
	for _, scenario := range []string{"set", "clear", "ignored requirement", "old server", "duplicate", "invalid mode", "conflicting clear"} {
		t.Run(scenario, func(t *testing.T) {
			main.ResetJSONOut(t)
			main.SetPreviewTestAuth(t)
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "PUT" || r.URL.Path != "/v1/apps/demo/bindings/release-policy-with-object-storage" {
					t.Errorf("unsafe route %s", r.URL)
				}
				if scenario == "old server" {
					http.NotFound(w, r)
					return
				}
				var req api.SetBindingReleasePolicyRequest
				if json.NewDecoder(r.Body).Decode(&req) != nil || req.ObjectStorageRequirements == nil {
					t.Error("explicit requirements lost")
				}
				now := time.Now().UTC()
				p := api.BindingReleasePolicy{AppID: "11111111-1111-4111-8111-111111111111", Scope: "default", Mode: "enforce", Revision: 1, MaxVerificationAge: "10m0s", UpdatedAt: &now, ObjectStorageRequirements: req.ObjectStorageRequirements}
				if scenario == "ignored requirement" {
					p.ObjectStorageRequirements = nil
				}
				main.WriteJSONTest(w, p)
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			var out bytes.Buffer
			old := (*main.OsStdout)
			(*main.OsStdout) = &out
			t.Cleanup(func() { (*main.OsStdout) = old })
			args := []string{"bindings", "release-policy", "set", "demo", "--mode", "enforce", "--expected-revision", "0", "--json"}
			if scenario == "clear" {
				args = append(args, "--clear-object-storage-requirements")
			} else {
				args = append(args, "--object-storage-require", "ASSETS=read_write")
			}
			switch scenario {
			case "duplicate":
				args = append(args, "--object-storage-require", "ASSETS=read")
			case "invalid mode":
				args = append(args, "--object-storage-require", "OTHER=delete")
			case "conflicting clear":
				args = append(args, "--clear-object-storage-requirements")
			}
			code := main.Run(args)
			wantSuccess := scenario == "set" || scenario == "clear"
			if (code == 0) != wantSuccess {
				t.Fatalf("wrong confirmation: %d %s", code, out.String())
			}
			if scenario == "duplicate" || scenario == "invalid mode" || scenario == "conflicting clear" {
				if calls != 0 {
					t.Fatal("invalid flags reached API")
				}
			} else if calls != 1 {
				t.Fatalf("unsafe fallback: %d calls", calls)
			}
		})
	}
}
