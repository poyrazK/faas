// adr: 428 — revision selection is app-scoped and confirmed before admission.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

const pinnedBindingDeployment = "a2b9cc53-907f-4b5c-88a4-fd0c21214556"

func captureBindingCLI(t *testing.T, url string, args ...string) (int, string, string) {
	t.Helper()
	t.Setenv("FAAS_API", url)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	oldOut, oldErr, oldJSON := osStdout, osStderr, jsonOutput
	var out, err bytes.Buffer
	osStdout, osStderr, jsonOutput = &out, &err, false
	defer func() { osStdout, osStderr, jsonOutput = oldOut, oldErr, oldJSON }()
	code := run(args)
	return code, out.String(), err.String()
}

func pinnedBindingProbeOutput(command, selection string) string {
	switch command {
	case api.AppTaskPostgresBindingProbeCommand:
		return `{"environment_key":"` + selection + `","environment":{"status":"passed"},"configuration":{"status":"passed"},"connection":{"status":"passed"},"query":{"status":"passed"}}`
	case api.AppTaskServiceBindingProbeCommand:
		return `{"service":"` + selection + `","dns":{"status":"passed"},"tls":{"status":"passed"},"authorization":{"status":"passed"},"routing":{"status":"passed"}}`
	default:
		body, _ := json.Marshal(passedObjectStorageCLIReport(selection))
		return string(body)
	}
}

func TestCmdBindingsVerifyDeploymentPinsAllProbeFamilies(t *testing.T) {
	for _, ref := range []string{pinnedBindingDeployment, "v12", strings.ReplaceAll(pinnedBindingDeployment, "-", "")} {
		for _, selection := range [][]string{{"--all"}, {"billing"}, {"--postgres", "DATABASE_URL"}, {"--object-storage", "ASSETS"}} {
			t.Run(ref+" "+strings.Join(selection, " "), func(t *testing.T) {
				inventoryCalls, resolutions, posts, polls := 0, 0, 0, 0
				responseID := pinnedBindingDeployment
				if len(ref) == 32 {
					responseID = ref
				}
				results := map[string]string{}
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					switch {
					case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api/deployments":
						resolutions++
						_ = json.NewEncoder(w).Encode(api.DeploymentListResponse{Items: []api.DeploymentResponse{{ID: pinnedBindingDeployment, Revision: 12}}})
					case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api/bindings":
						inventoryCalls++
						if r.URL.Query().Get("deployment_id") != pinnedBindingDeployment {
							t.Errorf("missing exact inventory target: %s", r.URL)
						}
						_ = json.NewEncoder(w).Encode(api.AppBindingInventory{App: "api", RequestedDeploymentID: responseID, VerificationDeploymentID: responseID, VerificationScope: "production", Complete: true, Bindings: []api.AppBindingInventoryItem{
							{Type: api.BindingTypeService, Name: "billing", Binding: "GREGALE_SERVICE_BILLING_URL", Scope: "app"},
							{Type: api.BindingTypePostgres, Name: "primary", Binding: "DATABASE_URL", Scope: "production"},
							{Type: api.BindingTypeObjectStorage, Name: "assets", Binding: "ASSETS", Scope: "production"},
						}})
					case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/api/tasks":
						posts++
						var request api.CreateAppTaskRequest
						if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
							t.Fatal(err)
						}
						if !sameBindingDeployment(request.VerificationDeploymentID, pinnedBindingDeployment) || !api.IsBindingVerificationCommand(request.Command, request.CommandShell) {
							t.Errorf("unbound probe: %+v", request)
						}
						id := "task-" + request.Command[1]
						results[id] = pinnedBindingProbeOutput(request.Command[0], request.Command[1])
						_ = json.NewEncoder(w).Encode(api.AppTaskResponse{ID: id, DeploymentID: responseID, DeploymentScope: "production", Status: api.AppTaskStatusQueued})
					case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/apps/api/tasks/"):
						polls++
						id := strings.TrimPrefix(r.URL.Path, "/v1/apps/api/tasks/")
						_ = json.NewEncoder(w).Encode(api.AppTaskResponse{ID: id, DeploymentID: responseID, DeploymentScope: "production", Status: api.AppTaskStatusSucceeded, ExitCode: new(int), StdoutTail: results[id]})
					default:
						t.Errorf("unexpected request: %s %s", r.Method, r.URL)
						http.NotFound(w, r)
					}
				}))
				defer srv.Close()
				args := append([]string{"bindings", "verify", "api", "--deployment", ref, "--poll-interval", "1ms", "--json"}, selection...)
				code, out, err := captureBindingCLI(t, srv.URL, args...)
				want := 1
				if selection[0] == "--all" {
					want = 3
				}
				wantResolutions := 0
				if ref == "v12" {
					wantResolutions = 1
				}
				if code != 0 || inventoryCalls != 1 || resolutions != wantResolutions || posts != want || polls != want {
					t.Fatalf("exit=%d reads=%d resolve=%d posts=%d polls=%d output=%s %s", code, inventoryCalls, resolutions, posts, polls, out, err)
				}
				if selection[0] == "--all" && !strings.Contains(out, responseID) {
					t.Fatalf("batch omitted target: %s", out)
				}
			})
		}
	}
}

func TestCmdBindingsVerifyDeploymentRejectsUnconfirmedSelection(t *testing.T) {
	for _, failure := range []string{"ignored selector", "wrong admission", "wrong poll"} {
		t.Run(failure, func(t *testing.T) {
			posts, cancels := 0, 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if strings.HasSuffix(r.URL.Path, "/bindings") {
					i := api.AppBindingInventory{App: "api", RequestedDeploymentID: pinnedBindingDeployment, VerificationDeploymentID: pinnedBindingDeployment, VerificationScope: "production", Bindings: []api.AppBindingInventoryItem{{Type: api.BindingTypeService, Name: "billing", Binding: "BILLING_URL", Scope: "app"}}}
					if failure == "ignored selector" {
						i.RequestedDeploymentID = ""
					}
					_ = json.NewEncoder(w).Encode(i)
					return
				}
				task := api.AppTaskResponse{ID: "task", DeploymentID: pinnedBindingDeployment, DeploymentScope: "production", Status: api.AppTaskStatusQueued}
				if r.Method == http.MethodDelete {
					cancels++
				} else if r.Method == http.MethodPost {
					posts++
					if failure == "wrong admission" {
						task.DeploymentID = "different"
					}
				} else {
					task.Status, task.ExitCode, task.StdoutTail = api.AppTaskStatusSucceeded, new(int), pinnedBindingProbeOutput(api.AppTaskServiceBindingProbeCommand, "billing")
					if failure == "wrong poll" {
						task.DeploymentScope = "staging"
					}
				}
				_ = json.NewEncoder(w).Encode(task)
			}))
			defer srv.Close()
			code, out, err := captureBindingCLI(t, srv.URL, "bindings", "verify", "api", "billing", "--deployment", pinnedBindingDeployment, "--poll-interval", "1ms")
			if code != 1 || failure == "ignored selector" && posts != 0 || failure == "wrong admission" && cancels != 1 {
				t.Fatalf("unsafe target: exit=%d posts=%d cancels=%d output=%s %s", code, posts, cancels, out, err)
			}
		})
	}
}

func TestCmdBindingsCheckDeploymentRequiresConfirmedMatchingEvidence(t *testing.T) {
	for _, confirmed := range []bool{true, false} {
		t.Run(map[bool]string{true: "confirmed", false: "ignored"}[confirmed], func(t *testing.T) {
			inventory := bindingCheckCLIInventory()
			inventory.VerificationDeploymentID = pinnedBindingDeployment
			inventory.Bindings[0].Verification.DeploymentID = pinnedBindingDeployment
			inventory.RuntimeFreshness.Deployments[0].DeploymentID = pinnedBindingDeployment
			if confirmed {
				inventory.RequestedDeploymentID = pinnedBindingDeployment
			}
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodGet || r.URL.Query().Get("deployment_id") != pinnedBindingDeployment {
					t.Errorf("unexpected check: %s %s", r.Method, r.URL)
				}
				_ = json.NewEncoder(w).Encode(inventory)
			}))
			defer srv.Close()
			code, out, err := captureBindingCLI(t, srv.URL, "bindings", "check", "api", "--deployment", pinnedBindingDeployment, "--json")
			report := decodeBindingCheckCLI(t, out)
			if report.Passed != confirmed || calls != 1 || report.ExpectedDeploymentID != pinnedBindingDeployment || code == 0 != confirmed || err != "" {
				t.Fatalf("exit=%d %+v output=%s", code, report, err)
			}
			if !confirmed && !slices.Contains(bindingCheckCLICodes(report), "deployment_selection_unconfirmed") {
				t.Fatalf("selector unsupported without blocker: %+v", report)
			}
		})
	}
}

func TestBindingDeploymentAdapterRejectsGenericTasks(t *testing.T) {
	c := &deploymentBindingProbeClient{slug: "api"}
	if _, err := c.CreateAppTask(context.Background(), "api", api.CreateAppTaskRequest{Command: []string{"echo", "hello"}}); err == nil {
		t.Fatal("generic command selected target")
	}
}
