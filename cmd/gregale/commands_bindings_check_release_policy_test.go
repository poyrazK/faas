// adr: 1029 — policy-aware CLI checks fail closed without scheduling provider work.
package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/bindingcheck"
)

const releaseCheckAppID = "11111111-1111-4111-8111-111111111111"
const releaseCheckDeploymentID = "22222222-2222-4222-8222-222222222222"

func storageReleaseCheckInventory(scope, deployment, mode string) api.AppBindingInventory {
	i := main.BindingCheckCLIInventory()
	i.Scope, i.VerificationDeploymentID = scope, releaseCheckDeploymentID
	if deployment != "" {
		i.RequestedDeploymentID = deployment
	}
	i.RuntimeFreshness.Deployments[0].DeploymentID = releaseCheckDeploymentID
	item := &i.Bindings[0]
	item.Access, item.Verification.DeploymentID = "read_write", releaseCheckDeploymentID
	item.Verification.ObjectStorageMode = mode
	if mode == "write" || mode == "read_write" {
		item.Verification.Checks = append(item.Verification.Checks, api.BindingVerificationCheck{Name: "upload", Status: "passed"}, api.BindingVerificationCheck{Name: "cleanup", Status: "passed"})
	}
	if mode == "read_write" {
		item.Verification.Checks = append(item.Verification.Checks, api.BindingVerificationCheck{Name: "download", Status: "passed"})
	}
	return i
}

func storageReleaseCheckPolicy() api.BindingReleasePolicy {
	at := time.Now().UTC().Add(-time.Hour)
	return api.BindingReleasePolicy{AppID: releaseCheckAppID, Scope: "production", Revision: 7, Mode: "enforce", MaxVerificationAge: "5m", UpdatedAt: &at,
		ObjectStorageRequirements: map[string]string{"ASSETS": "read_write"}}
}

func runStorageReleaseCheckCLI(t *testing.T, handler http.HandlerFunc, args ...string) (int, main.BindingReleasePolicyCheckReport, string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("read-only release check mutated the API: %s %s", r.Method, r.URL)
			http.Error(w, "unexpected mutation", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		handler(w, r)
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	oldOut, oldErr, oldJSON := (*main.OsStdout), (*main.OsStderr), (*main.JsonOutput)
	var out, errs bytes.Buffer
	(*main.OsStdout), (*main.OsStderr), (*main.JsonOutput) = &out, &errs, false
	defer func() { (*main.OsStdout), (*main.OsStderr), (*main.JsonOutput) = oldOut, oldErr, oldJSON }()
	command := append([]string{"bindings", "check", "api", "--json"}, args...)
	code := main.Run(command)
	var report main.BindingReleasePolicyCheckReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("expected one JSON result, exit=%d: %v; out=%s err=%s", code, err, out.String(), errs.String())
	}
	return code, report, errs.String()
}

func serveStorageReleaseCheckApp(w http.ResponseWriter) {
	_ = json.NewEncoder(w).Encode(api.AppResponse{ID: releaseCheckAppID, Slug: "api"})
}

func TestBindingsCheckReleasePolicyRequiresStoredStorageEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, mode, requirement, blocker string
		args                             []string
		pass                             bool
	}{
		{"legacy check", "", "read_write", "", nil, true},
		{"explicitly disabled", "", "read_write", "", []string{"--release-policy=false"}, true},
		{"listing is insufficient", "", "read_write", "object_storage_operations_missing", []string{"--release-policy"}, false},
		{"upload is insufficient", "write", "read_write", "object_storage_operations_missing", []string{"--release-policy"}, false},
		{"full transfer", "read_write", "read_write", "", []string{"--release-policy"}, true},
		{"read listing", "", "read", "", []string{"--release-policy"}, true},
		{"combined requirements", "write", "read", "object_storage_operations_missing", []string{"--release-policy", "--object-storage-require", "ASSETS=write"}, false},
		{"combined requirements pass", "read_write", "read", "", []string{"--release-policy", "--object-storage-require", "ASSETS=write"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var inventories, policies, apps atomic.Int32
			stored := storageReleaseCheckPolicy()
			stored.ObjectStorageRequirements["ASSETS"] = tc.requirement
			code, report, stderr := runStorageReleaseCheckCLI(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/apps/api":
					apps.Add(1)
					serveStorageReleaseCheckApp(w)
				case "/v1/apps/api/bindings":
					n := inventories.Add(1)
					if n > 1 && (r.URL.Query().Get("scope") != "production" || r.URL.Query().Get("deployment_id") != releaseCheckDeploymentID) {
						t.Errorf("unpinned policy evaluation: %s", r.URL)
					}
					_ = json.NewEncoder(w).Encode(storageReleaseCheckInventory(r.URL.Query().Get("scope"), r.URL.Query().Get("deployment_id"), tc.mode))
				case "/v1/apps/api/bindings/release-policy":
					policies.Add(1)
					if r.URL.Query().Get("scope") != "production" {
						t.Errorf("wrong policy scope: %s", r.URL)
					}
					_ = json.NewEncoder(w).Encode(stored)
				default:
					t.Errorf("unexpected request: %s", r.URL)
					http.NotFound(w, r)
				}
			}, tc.args...)
			if (code == 0) != tc.pass || report.Passed != tc.pass || stderr != "" {
				t.Fatalf("exit=%d report=%+v stderr=%s", code, report, stderr)
			}
			if tc.blocker != "" && !slices.Contains(main.BindingCheckCLICodes(report.Report), tc.blocker) {
				t.Fatalf("required operations were not enforced: %+v", report)
			}
			if len(tc.args) == 0 || tc.args[0] == "--release-policy=false" {
				if inventories.Load() != 1 || policies.Load() != 0 || apps.Load() != 0 || report.ReleasePolicy != nil {
					t.Fatal("legacy check unexpectedly read policy")
				}
			} else if inventories.Load() != 2 || policies.Load() != 2 || apps.Load() != 1 || !report.ReleasePolicyConfirmed || report.ReleasePolicy.Revision != 7 || report.ExpectedDeploymentID != releaseCheckDeploymentID {
				t.Fatalf("policy or deployment was not confirmed: %+v, inventories=%d policies=%d apps=%d", report, inventories.Load(), policies.Load(), apps.Load())
			}
		})
	}
}

func TestBindingsCheckReleasePolicyCannotWeakenStoredConstraints(t *testing.T) {
	for _, scenario := range []string{"stored age", "stricter local age", "stored ACK", "local ACK", "unsupported waiver", "additional prefix", "policy off"} {
		t.Run(scenario, func(t *testing.T) {
			stored := storageReleaseCheckPolicy()
			args := []string{"--release-policy"}
			mode, wantCode := "read_write", ""
			switch scenario {
			case "stored age":
				stored.MaxVerificationAge = "30s"
				args = append(args, "--max-verification-age", "15m")
				wantCode = "verification_expired"
			case "stricter local age":
				args = append(args, "--max-verification-age", "10s")
				wantCode = "verification_expired"
			case "stored ACK":
				stored.RequireApplicationAck = true
				args = append(args, "--require-application-ack=false")
				wantCode = "application_adoption_unknown"
			case "local ACK":
				args = append(args, "--require-application-ack")
				wantCode = "application_adoption_unknown"
			case "unsupported waiver":
				args = append(args, "--allow-unsupported")
				wantCode = "verification_unsupported"
			case "additional prefix":
				args = append(args, "--object-storage-require", "OTHER=read")
				wantCode = "object_storage_binding_missing"
			case "policy off":
				stored.Mode, stored.RequireApplicationAck, stored.MaxVerificationAge = "off", true, "30s"
				mode = ""
			}
			code, report, _ := runStorageReleaseCheckCLI(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/apps/api":
					serveStorageReleaseCheckApp(w)
				case "/v1/apps/api/bindings/release-policy":
					_ = json.NewEncoder(w).Encode(stored)
				case "/v1/apps/api/bindings":
					i := storageReleaseCheckInventory(r.URL.Query().Get("scope"), r.URL.Query().Get("deployment_id"), mode)
					if scenario == "unsupported waiver" {
						i.Bindings = append(i.Bindings, api.AppBindingInventoryItem{Type: api.BindingTypeOutbound, Name: "external", Binding: "OUT", Scope: "production", State: "enabled"})
					}
					_ = json.NewEncoder(w).Encode(i)
				default:
					t.Errorf("unexpected request: %s", r.URL)
				}
			}, args...)
			if !report.ReleasePolicyConfirmed || (code == 0) != (scenario == "policy off") || report.Passed != (scenario == "policy off") {
				t.Fatalf("policy constraints lost: code=%d %+v", code, report)
			}
			if wantCode != "" && !slices.Contains(main.BindingCheckCLICodes(report.Report), wantCode) {
				t.Fatalf("missing %s: %+v", wantCode, report)
			}
			if scenario == "additional prefix" && (report.ObjectStorageRequirements["OTHER"] != "read" || report.ObjectStorageRequirements["ASSETS"] != "read_write") {
				t.Fatal("local flags replaced stored requirements")
			}
			if scenario == "unsupported waiver" && report.AllowUnsupported {
				t.Fatal("enforced policy allowed a local waiver")
			}
		})
	}
}

func TestBindingsCheckReleasePolicyFailsOnUnconfirmedPolicy(t *testing.T) {
	for _, scenario := range []string{"old server", "forbidden", "revision changed", "requirements changed without revision", "mode changed", "confirmation failed", "invalid policy", "wrong policy app", "wrong scope", "unknown operation", "wrong app response", "wrong deployment echo", "wrong selected scope", "wrong confirmation app", "wrong confirmation scope"} {
		t.Run(scenario, func(t *testing.T) {
			stored := storageReleaseCheckPolicy()
			var policyCalls atomic.Int32
			args := []string{"--release-policy"}
			wantCode := "release_policy_changed"
			switch scenario {
			case "old server", "forbidden", "confirmation failed":
				wantCode = "release_policy_unavailable"
			case "invalid policy", "wrong policy app", "wrong scope", "unknown operation", "wrong confirmation app", "wrong confirmation scope":
				wantCode = "release_policy_invalid"
			case "wrong app response":
				wantCode = "release_policy_app_unconfirmed"
			case "wrong deployment echo", "wrong selected scope":
				wantCode = "release_policy_selection_invalid"
				args = append(args, "--deployment", releaseCheckDeploymentID, "--scope", "production")
			}
			code, report, _ := runStorageReleaseCheckCLI(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/apps/api":
					if scenario == "wrong app response" {
						_ = json.NewEncoder(w).Encode(api.AppResponse{ID: releaseCheckAppID, Slug: "other"})
					} else {
						serveStorageReleaseCheckApp(w)
					}
				case "/v1/apps/api/bindings":
					i := storageReleaseCheckInventory(r.URL.Query().Get("scope"), r.URL.Query().Get("deployment_id"), "read_write")
					if scenario == "wrong deployment echo" {
						i.RequestedDeploymentID = "33333333-3333-4333-8333-333333333333"
					}
					if scenario == "wrong selected scope" {
						i.VerificationScope = "staging"
					}
					_ = json.NewEncoder(w).Encode(i)
				case "/v1/apps/api/bindings/release-policy":
					n := policyCalls.Add(1)
					p := main.CopyBindingReleasePolicy(stored)
					if scenario == "old server" || scenario == "forbidden" || scenario == "confirmation failed" && n > 1 {
						status := http.StatusNotFound
						if scenario == "forbidden" {
							status = http.StatusForbidden
						}
						w.WriteHeader(status)
						_ = json.NewEncoder(w).Encode(api.NewProblem(status, api.CodeNotFound, "Unavailable", "Policy unavailable"))
						return
					}
					if n > 1 {
						switch scenario {
						case "revision changed":
							p.Revision++
						case "requirements changed without revision":
							p.ObjectStorageRequirements["ASSETS"] = "read"
						case "mode changed":
							p.Mode = "off"
						case "wrong confirmation app":
							p.AppID = "33333333-3333-4333-8333-333333333333"
						case "wrong confirmation scope":
							p.Scope = "staging"
						}
					}
					switch scenario {
					case "invalid policy":
						p.MaxVerificationAge = "forever"
					case "wrong policy app":
						p.AppID = "33333333-3333-4333-8333-333333333333"
					case "wrong scope":
						p.Scope = "staging"
					case "unknown operation":
						p.ObjectStorageRequirements["ASSETS"] = "delete"
					}
					_ = json.NewEncoder(w).Encode(p)
				default:
					t.Errorf("unexpected request: %s", r.URL)
				}
			}, args...)
			if code == 0 || report.Passed || report.ReleasePolicyConfirmed || !slices.Contains(main.BindingCheckCLICodes(report.Report), wantCode) {
				t.Fatalf("unconfirmed policy produced a pass: exit=%d %+v", code, report)
			}
			if (scenario == "old server" || scenario == "forbidden") && policyCalls.Load() != 1 {
				t.Fatalf("unexpected fallback: %d policy reads", policyCalls.Load())
			}
		})
	}
}

func TestBindingsCheckReleasePolicyWaitsForStorageCanaryAndStopsOnPolicyChange(t *testing.T) {
	for _, scenario := range []string{"completes", "policy changes", "missing probe", "failed cleanup", "timeout"} {
		t.Run(scenario, func(t *testing.T) {
			stored := storageReleaseCheckPolicy()
			var inventoryCalls, policyCalls atomic.Int32
			args := []string{"--release-policy", "--wait", "--timeout", "1s", "--poll-interval", "1ms"}
			if scenario == "timeout" {
				args = []string{"--release-policy", "--wait", "--timeout", "100ms", "--poll-interval", "1s"}
			}
			code, report, _ := runStorageReleaseCheckCLI(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/apps/api":
					serveStorageReleaseCheckApp(w)
				case "/v1/apps/api/bindings/release-policy":
					n := policyCalls.Add(1)
					p := main.CopyBindingReleasePolicy(stored)
					if scenario == "policy changes" && n >= 3 {
						p.Revision++
					}
					_ = json.NewEncoder(w).Encode(p)
				case "/v1/apps/api/bindings":
					n := inventoryCalls.Add(1)
					i := storageReleaseCheckInventory(r.URL.Query().Get("scope"), r.URL.Query().Get("deployment_id"), "read_write")
					if n <= 2 || scenario == "timeout" {
						i.Bindings[0].VerificationStatus = "unknown"
						i.Bindings[0].Verification.Result, i.Bindings[0].Verification.Reason = "unknown", "probe_pending"
						i.Bindings[0].Verification.ObjectStorageMode = ""
						i.Bindings[0].Verification.Checks = nil
					}
					if scenario == "missing probe" {
						i.Bindings[0].Verification = nil
					}
					if scenario == "failed cleanup" {
						i = storageReleaseCheckInventory(r.URL.Query().Get("scope"), r.URL.Query().Get("deployment_id"), "read_write")
						i.Bindings[0].Verification.Checks[6].Status = "failed"
					}
					_ = json.NewEncoder(w).Encode(i)
				default:
					t.Errorf("unexpected request: %s", r.URL)
				}
			}, args...)
			if (code == 0) != (scenario == "completes") || report.Passed != (scenario == "completes") {
				t.Fatalf("exit=%d report=%+v inventories=%d policies=%d", code, report, inventoryCalls.Load(), policyCalls.Load())
			}
			if scenario == "policy changes" && (report.ReleasePolicyConfirmed || !slices.Contains(main.BindingCheckCLICodes(report.Report), "release_policy_changed")) {
				t.Fatal("wait accepted a changed release policy")
			}
			if (scenario == "missing probe" || scenario == "failed cleanup") && inventoryCalls.Load() != 2 {
				t.Fatal("terminal evidence unnecessarily kept polling")
			}
			if scenario == "timeout" && report.ReleasePolicyConfirmed {
				t.Fatal("timed-out check retained a policy confirmation")
			}
		})
	}
}

func TestBindingsCheckReleasePolicyFinalReadTimeoutCannotReturnPass(t *testing.T) {
	stored := storageReleaseCheckPolicy()
	var policies atomic.Int32
	code, report, stderr := runStorageReleaseCheckCLI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/apps/api":
			serveStorageReleaseCheckApp(w)
		case "/v1/apps/api/bindings":
			_ = json.NewEncoder(w).Encode(storageReleaseCheckInventory(r.URL.Query().Get("scope"), r.URL.Query().Get("deployment_id"), "read_write"))
		case "/v1/apps/api/bindings/release-policy":
			if policies.Add(1) > 1 {
				<-r.Context().Done()
				return
			}
			_ = json.NewEncoder(w).Encode(stored)
		default:
			t.Errorf("unexpected request: %s", r.URL)
		}
	}, "--release-policy", "--timeout", "1s")
	if code == 0 || report.Passed || report.ReleasePolicyConfirmed || policies.Load() != 2 || !slices.Contains(main.BindingCheckCLICodes(report.Report), "release_policy_unavailable") || !strings.Contains(stderr, "Binding preflight failed:") {
		t.Fatalf("timeout returned a policy pass: code=%d %+v stderr=%s", code, report, stderr)
	}
}

func TestBindingsCheckReleasePolicyFreshInventoryFailureRetainsEffectiveConstraints(t *testing.T) {
	stored := storageReleaseCheckPolicy()
	stored.RequireApplicationAck = true
	var inventories, policies atomic.Int32
	code, report, _ := runStorageReleaseCheckCLI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/apps/api":
			serveStorageReleaseCheckApp(w)
		case "/v1/apps/api/bindings":
			if inventories.Add(1) > 1 {
				http.Error(w, "inventory unavailable", http.StatusServiceUnavailable)
				return
			}
			_ = json.NewEncoder(w).Encode(storageReleaseCheckInventory("", "", ""))
		case "/v1/apps/api/bindings/release-policy":
			policies.Add(1)
			_ = json.NewEncoder(w).Encode(stored)
		default:
			t.Errorf("unexpected request: %s", r.URL)
		}
	}, "--release-policy")
	if code == 0 || report.Passed || report.ReleasePolicyConfirmed || policies.Load() != 1 || !slices.Contains(main.BindingCheckCLICodes(report.Report), "release_policy_check_incomplete") {
		t.Fatalf("failed inventory produced a policy pass: code=%d %+v", code, report)
	}
	if report.ObjectStorageRequirements["ASSETS"] != "read_write" || !report.RequireApplicationAck || report.MaxVerificationAge != "5m0s" || len(report.Bindings) != 0 || !report.InventoryGeneratedAt.IsZero() {
		t.Fatalf("failed read exposed local-only evidence or lost effective constraints: %+v", report)
	}
}

type releaseCheckCancelClient struct {
	bindingCheckWaitClient
	policyRead func(context.Context, string, string) (api.BindingReleasePolicy, error)
}

func (c releaseCheckCancelClient) GetApp(context.Context, string) (api.AppResponse, error) {
	return api.AppResponse{ID: releaseCheckAppID, Slug: "api"}, nil
}

func (c releaseCheckCancelClient) GetBindingReleasePolicy(ctx context.Context, app, scope string) (api.BindingReleasePolicy, error) {
	return c.policyRead(ctx, app, scope)
}

func TestBindingsCheckReleasePolicyCancellationAtFinalReadCannotPass(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stored, calls := storageReleaseCheckPolicy(), 0
	client := releaseCheckCancelClient{
		bindingCheckWaitClient: bindingCheckWaitClient{read: func(_ context.Context, _ string, scope, deployment string) (api.AppBindingInventory, error) {
			return storageReleaseCheckInventory(scope, deployment, "read_write"), nil
		}},
		policyRead: func(context.Context, string, string) (api.BindingReleasePolicy, error) {
			calls++
			if calls == 2 {
				// Completion can coincide with interruption even if the response was decoded.
				cancel()
			}
			return main.CopyBindingReleasePolicy(stored), nil
		},
	}
	report, err := main.PollBindingCheckReleasePolicy(ctx, client, bindingcheck.Policy{App: "api", MaxVerificationAge: time.Minute * 10}, false, time.Second)
	if !errors.Is(err, context.Canceled) || report.Passed || report.ReleasePolicyConfirmed || calls != 2 || !slices.Contains(main.BindingCheckCLICodes(report.Report), "release_policy_check_incomplete") {
		t.Fatalf("cancelled final confirmation passed: report=%+v err=%v calls=%d", report, err, calls)
	}
}
