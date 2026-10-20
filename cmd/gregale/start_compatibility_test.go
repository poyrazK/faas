package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestStartCompatibilityBlocksMutations(t *testing.T) {
	for _, outcome := range []string{"old API", "method missing", "not implemented", "old gateway", "unconfigured gateway", "outage", "new protocol", "malformed", "denied"} {
		t.Run(outcome, func(t *testing.T) {
			stdout, stderr := main.StartTestEnvironment(t)
			source := main.StartTestSource(t)
			main.WithCwd(t, source)
			var writes atomic.Int32
			main.StartTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					writes.Add(1)
				}
				http.NotFound(w, r)
			}, func(w http.ResponseWriter, r *http.Request) {
				result := api.DeploymentResponseCheckCapabilities{ProtocolVersion: api.DeploymentResponseCheckProtocolVersion}
				switch outcome {
				case "old API":
					http.NotFound(w, r)
					return
				case "method missing":
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				case "not implemented":
					w.WriteHeader(http.StatusNotImplemented)
					return
				case "old gateway":
					result.UnavailableReason = api.DeploymentResponseCheckGatewayUnsupported
				case "unconfigured gateway":
					result.UnavailableReason = api.DeploymentResponseCheckGatewayUnconfigured
				case "outage":
					result.UnavailableReason = api.DeploymentResponseCheckGatewayUnavailable
				case "new protocol":
					result.ProtocolVersion++
					result.Available = true
				case "malformed":
					_, _ = w.Write([]byte("not JSON"))
					return
				case "denied":
					w.WriteHeader(http.StatusForbidden)
					return
				}
				_ = json.NewEncoder(w).Encode(result)
			})
			main.PipeStdin(t, "\ny\n")
			if code := main.CmdStart(nil); code == 0 || writes.Load() != 0 || strings.Contains(stdout.String(), "App ready") || !strings.Contains(stderr(), "verification") {
				t.Fatalf("incompatible launch changed remote state: code=%d writes=%d output=%s error=%s", code, writes.Load(), stdout, stderr())
			}
		})
	}
}

func TestStartCompatibilityRecheckedBeforeAppSecretsOrUpload(t *testing.T) {
	stdout, stderr := main.StartTestEnvironment(t)
	source := main.StartTestSource(t)
	main.WithCwd(t, source)
	file := filepath.Join(t.TempDir(), "credentials.env")
	main.WriteFile(t, filepath.Dir(file), filepath.Base(file), "FAAS_TEST_ALPHA=only-for-this-fixture\n")
	var reads, writes atomic.Int32
	main.StartTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes.Add(1)
		}
		http.NotFound(w, r)
	}, func(w http.ResponseWriter, _ *http.Request) {
		result := api.DeploymentResponseCheckCapabilities{ProtocolVersion: api.DeploymentResponseCheckProtocolVersion, Available: reads.Add(1) == 1}
		if !result.Available {
			result.UnavailableReason = api.DeploymentResponseCheckGatewayUnavailable
		}
		_ = json.NewEncoder(w).Encode(result)
	})
	runner := main.StartTestRunner(t, source, "y\n")
	(*runner.Deploy()) = main.CmdDeployTarballToExisting
	(*runner.SecretsFile()) = file
	code, submitted := runner.ReviewAndDeploy()
	if code == 0 || submitted || reads.Load() != 2 || writes.Load() != 0 || (*runner.Session()).DeploymentID != "" {
		t.Fatalf("stale compatibility allowed mutation: code=%d submitted=%t reads=%d writes=%d output=%s error=%s", code, submitted, reads.Load(), writes.Load(), stdout, stderr())
	}
}

func TestStartCompatibilityFailurePreservesPreviousLaunch(t *testing.T) {
	main.StartTestEnvironment(t)
	source := main.StartTestSource(t)
	main.WithCwd(t, source)
	main.StartTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("failed preflight reached an app route")
		http.NotFound(w, r)
	}, http.NotFound)
	runner := main.StartTestRunner(t, source, "")
	(*runner.Session()).AppID, (*runner.Session()).DeploymentID, (*runner.Session()).Status = main.StartResponseAppID, main.StartResponseDeploymentID, main.StatusLive
	if err := main.SaveStartSession((*runner.StatePath()), (*runner.Session())); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile((*runner.StatePath()))
	if err != nil {
		t.Fatal(err)
	}
	main.PipeStdin(t, "2\n1\ny\n")
	if code := main.CmdStart(nil); code == 0 {
		t.Fatal("incompatible fresh launch reported success")
	}
	after, err := os.ReadFile((*runner.StatePath()))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("failed preflight overwrote the accepted launch")
	}
}

func TestStartCompatibilityRecoveryKeepsAcceptedDeployment(t *testing.T) {
	for _, mode := range []string{"unsupported", "outage then recovered"} {
		t.Run(mode, func(t *testing.T) {
			stdout, stderr := main.StartTestEnvironment(t)
			var probes, checks atomic.Int32
			main.StartTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/apps/first-app":
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: main.StartResponseAppID, RequireAuthn: true})
				case "/v1/apps/first-app/deployments/" + main.StartResponseDeploymentID + "/response-check":
					checks.Add(1)
					_ = json.NewEncoder(w).Encode(api.DeploymentResponseCheckResult{AppID: main.StartResponseAppID, DeploymentID: main.StartResponseDeploymentID, Scope: "default", InstanceID: main.StartResponseInstanceID, HTTPStatus: 200, Body: "accepted response"})
				default:
					t.Errorf("compatibility recovery submitted work or read app logs: %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			}, func(w http.ResponseWriter, r *http.Request) {
				count := probes.Add(1)
				if mode == "unsupported" {
					http.NotFound(w, r)
					return
				}
				result := api.DeploymentResponseCheckCapabilities{ProtocolVersion: api.DeploymentResponseCheckProtocolVersion, Available: count > 1}
				if !result.Available {
					result.UnavailableReason = api.DeploymentResponseCheckGatewayUnavailable
				}
				_ = json.NewEncoder(w).Encode(result)
			})
			runner := main.StartTestRunner(t, main.StartTestSource(t), "\n")
			(*runner.Session()).AppID, (*runner.Session()).DeploymentID, (*runner.Session()).Status = main.StartResponseAppID, main.StartResponseDeploymentID, main.StatusLive
			(*runner.Deploy()) = func(context.Context, []string, bool, ...main.DeployExecution) int {
				t.Fatal("recovery deployed another revision")
				return 1
			}
			if err := main.SaveStartSession((*runner.StatePath()), (*runner.Session())); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile((*runner.StatePath()))
			code := runner.CheckFirstResponse("/", "")
			after, _ := os.ReadFile((*runner.StatePath()))
			if (code == 0) != (mode != "unsupported") || !bytes.Equal(before, after) || strings.Contains(stdout.String(), "Check another GET path") || strings.Contains(stdout.String(), "Show recent app logs") {
				t.Fatalf("bad platform failure recovery: code=%d output=%s error=%s", code, stdout, stderr())
			}
			if mode == "unsupported" {
				if checks.Load() != 0 || probes.Load() != 1 || strings.Contains(stdout.String(), "Retry this GET check") || !strings.Contains(stdout.String(), main.StartResponseDeploymentID) || !strings.Contains(stderr(), "update") {
					t.Fatal("unsupported service offered ineffective retries or lost recovery guidance")
				}
			} else if checks.Load() != 1 || probes.Load() != 2 || (*runner.LastRequest()) == nil {
				t.Fatal("retry did not check the same accepted deployment after recovery")
			}
		})
	}
}
