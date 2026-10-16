package main

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

	"github.com/onebox-faas/faas/pkg/api"
)

func TestStartCompatibilityBlocksMutations(t *testing.T) {
	for _, outcome := range []string{"old API", "method missing", "not implemented", "old gateway", "unconfigured gateway", "outage", "new protocol", "malformed", "denied"} {
		t.Run(outcome, func(t *testing.T) {
			stdout, stderr := startTestEnvironment(t)
			source := startTestSource(t)
			withCwd(t, source)
			var writes atomic.Int32
			startTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
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
			pipeStdin(t, "\ny\n")
			if code := cmdStart(nil); code == 0 || writes.Load() != 0 || strings.Contains(stdout.String(), "App ready") || !strings.Contains(stderr(), "verification") {
				t.Fatalf("incompatible launch changed remote state: code=%d writes=%d output=%s error=%s", code, writes.Load(), stdout, stderr())
			}
		})
	}
}

func TestStartCompatibilityRecheckedBeforeAppSecretsOrUpload(t *testing.T) {
	stdout, stderr := startTestEnvironment(t)
	source := startTestSource(t)
	withCwd(t, source)
	file := filepath.Join(t.TempDir(), "credentials.env")
	writeFile(t, filepath.Dir(file), filepath.Base(file), "FAAS_TEST_ALPHA=only-for-this-fixture\n")
	var reads, writes atomic.Int32
	startTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
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
	runner := startTestRunner(t, source, "y\n")
	runner.deploy = cmdDeployTarballToExisting
	runner.secretsFile = file
	code, submitted := runner.reviewAndDeploy()
	if code == 0 || submitted || reads.Load() != 2 || writes.Load() != 0 || runner.session.DeploymentID != "" {
		t.Fatalf("stale compatibility allowed mutation: code=%d submitted=%t reads=%d writes=%d output=%s error=%s", code, submitted, reads.Load(), writes.Load(), stdout, stderr())
	}
}

func TestStartCompatibilityFailurePreservesPreviousLaunch(t *testing.T) {
	startTestEnvironment(t)
	source := startTestSource(t)
	withCwd(t, source)
	startTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("failed preflight reached an app route")
		http.NotFound(w, r)
	}, http.NotFound)
	runner := startTestRunner(t, source, "")
	runner.session.AppID, runner.session.DeploymentID, runner.session.Status = startResponseAppID, startResponseDeploymentID, statusLive
	if err := saveStartSession(runner.statePath, runner.session); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(runner.statePath)
	if err != nil {
		t.Fatal(err)
	}
	pipeStdin(t, "2\n1\ny\n")
	if code := cmdStart(nil); code == 0 {
		t.Fatal("incompatible fresh launch reported success")
	}
	after, err := os.ReadFile(runner.statePath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("failed preflight overwrote the accepted launch")
	}
}

func TestStartCompatibilityRecoveryKeepsAcceptedDeployment(t *testing.T) {
	for _, mode := range []string{"unsupported", "outage then recovered"} {
		t.Run(mode, func(t *testing.T) {
			stdout, stderr := startTestEnvironment(t)
			var probes, checks atomic.Int32
			startTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/apps/first-app":
					_ = json.NewEncoder(w).Encode(api.AppResponse{ID: startResponseAppID, RequireAuthn: true})
				case "/v1/apps/first-app/deployments/" + startResponseDeploymentID + "/response-check":
					checks.Add(1)
					_ = json.NewEncoder(w).Encode(api.DeploymentResponseCheckResult{AppID: startResponseAppID, DeploymentID: startResponseDeploymentID, Scope: "default", InstanceID: startResponseInstanceID, HTTPStatus: 200, Body: "accepted response"})
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
			runner := startTestRunner(t, startTestSource(t), "\n")
			runner.session.AppID, runner.session.DeploymentID, runner.session.Status = startResponseAppID, startResponseDeploymentID, statusLive
			runner.deploy = func(context.Context, []string, bool, ...deployExecution) int {
				t.Fatal("recovery deployed another revision")
				return 1
			}
			if err := saveStartSession(runner.statePath, runner.session); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(runner.statePath)
			code := runner.checkFirstResponse("/", "")
			after, _ := os.ReadFile(runner.statePath)
			if (code == 0) != (mode != "unsupported") || !bytes.Equal(before, after) || strings.Contains(stdout.String(), "Check another GET path") || strings.Contains(stdout.String(), "Show recent app logs") {
				t.Fatalf("bad platform failure recovery: code=%d output=%s error=%s", code, stdout, stderr())
			}
			if mode == "unsupported" {
				if checks.Load() != 0 || probes.Load() != 1 || strings.Contains(stdout.String(), "Retry this GET check") || !strings.Contains(stdout.String(), startResponseDeploymentID) || !strings.Contains(stderr(), "update") {
					t.Fatal("unsupported service offered ineffective retries or lost recovery guidance")
				}
			} else if checks.Load() != 1 || probes.Load() != 2 || runner.lastRequest == nil {
				t.Fatal("retry did not check the same accepted deployment after recovery")
			}
		})
	}
}
