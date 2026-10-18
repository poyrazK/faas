package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestStartPublicLaunchChecksExactDeploymentWhenPublicIdentityUnavailable(t *testing.T) {
	for _, phase := range []string{"first launch", "update", "resumed first launch", "resumed update"} {
		for _, outcome := range []string{"success", "old server", "wrong deployment", "application denied", "public denied", "edge health", "identified old deployment", "identified accepted deployment"} {
			t.Run(phase+"/"+outcome, func(t *testing.T) {
				stdout, _ := startTestEnvironment(t)
				var publicCalls, ownerCalls atomic.Int32
				public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					publicCalls.Add(1)
					if r.Header.Get("Authorization") != "" {
						t.Error("public check received an account credential")
					}
					if outcome == "edge health" {
						w.Header().Set("X-Faas-Health-Source", "edge")
					}
					if outcome == "identified old deployment" {
						w.Header().Set(api.DeploymentIDHeader, startResponseInstanceID)
					}
					if outcome == "identified accepted deployment" {
						w.Header().Set(api.DeploymentIDHeader, startResponseDeploymentID)
					}
					if outcome == "public denied" {
						w.WriteHeader(http.StatusForbidden)
					}
					_, _ = w.Write([]byte("previous revision body"))
				}))
				t.Cleanup(public.Close)
				startTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/v1/apps/first-app":
						_ = json.NewEncoder(w).Encode(api.AppResponse{ID: startResponseAppID, CanonicalURL: public.URL})
					case "/v1/apps/first-app/deployments/" + startResponseDeploymentID + "/response-check":
						ownerCalls.Add(1)
						if outcome == "old server" {
							http.NotFound(w, r)
							return
						}
						var request api.DeploymentResponseCheckRequest
						if json.NewDecoder(r.Body).Decode(&request) != nil || request.AppID != startResponseAppID || request.Scope != "default" || request.ProjectID != "" || request.EnvironmentID != "" || request.Path != "/ready?temporary=1" || r.Header.Get("Authorization") != "Bearer "+loadToken() {
							t.Error("owner check changed the reviewed target or omitted authentication")
						}
						result := api.DeploymentResponseCheckResult{AppID: request.AppID, Scope: request.Scope, DeploymentID: startResponseDeploymentID, InstanceID: startResponseInstanceID, HTTPStatus: http.StatusOK, Body: "selected deployment"}
						if outcome == "wrong deployment" {
							result.DeploymentID = startResponseInstanceID
						}
						if outcome == "application denied" {
							result.HTTPStatus = http.StatusUnauthorized
						}
						_ = json.NewEncoder(w).Encode(result)
					default:
						if r.Method != http.MethodGet {
							t.Errorf("check submitted another deployment or invocation: %s %s", r.Method, r.URL.Path)
						}
						http.NotFound(w, r)
					}
				})
				runner := startTestRunner(t, startTestSource(t), "")
				runner.session.AppID, runner.session.DeploymentID = startResponseAppID, startResponseDeploymentID
				runner.projectUpdates = phase == "update"
				runner.session.RequireDeploymentEvidence = phase == "resumed update"
				if strings.HasPrefix(phase, "resumed") {
					if err := saveStartSession(runner.statePath, runner.session); err != nil {
						t.Fatal(err)
					}
					recovered := startTestRunner(t, runner.session.ScopePath, "\n")
					resume, proceed, err := recovered.chooseRecovery()
					if err != nil || !resume || !proceed {
						t.Fatalf("could not resume accepted launch: resume=%t proceed=%t err=%v", resume, proceed, err)
					}
					runner = recovered
				}
				code := runner.sendTestRequest("/ready?temporary=1", "")
				success := outcome == "success" || outcome == "identified accepted deployment"
				if (code == 0) != success || publicCalls.Load() != 1 {
					t.Fatalf("code=%d public calls=%d output=%s", code, publicCalls.Load(), stdout)
				}
				wantOwner := int32(1)
				if outcome == "public denied" || outcome == "edge health" || strings.HasPrefix(outcome, "identified") {
					wantOwner = 0
				}
				if ownerCalls.Load() != wantOwner {
					t.Fatalf("owner calls=%d want=%d", ownerCalls.Load(), wantOwner)
				}
				if outcome == "success" && (runner.lastRequest == nil || !runner.lastRequest.publicDeploymentCheck || runner.lastRequest.servedDeployment != startResponseDeploymentID || runner.lastRequest.body != "selected deployment" || strings.Contains(stdout.String(), "previous revision body") || strings.Contains(stdout.String(), "Verified URL") || !strings.Contains(stdout.String(), "owner-authorized check")) {
					t.Fatalf("owner check claimed public deployment evidence: %s", stdout)
				}
				if !success && (runner.lastRequest != nil || strings.Contains(stdout.String(), "Your app answered.") || strings.Contains(stdout.String(), "Verified URL")) {
					t.Fatalf("unverified launch was reported as successful: %s", stdout)
				}
			})
		}
	}
}

func TestStartFirstResponseCannotVerifyWithoutDeploymentIdentity(t *testing.T) {
	stdout, _ := startTestEnvironment(t)
	runner := startTestRunner(t, startTestSource(t), "")
	runner.session.AppID, runner.session.DeploymentID = startResponseAppID, startResponseDeploymentID
	result := startRequestResult{path: "/", httpStatus: http.StatusOK, body: "an unidentified response"}
	if code := runner.verifyRequest(result, ""); code == 0 || runner.lastRequest != nil || strings.Contains(stdout.String(), "Your app answered.") {
		t.Fatalf("first launch verified without accepted-deployment evidence: code=%d output=%s", code, stdout)
	}
}
