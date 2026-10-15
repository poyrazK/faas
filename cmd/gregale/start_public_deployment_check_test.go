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

func TestStartPublicUpdateChecksExactDeploymentWhenPublicIdentityUnavailable(t *testing.T) {
	for _, outcome := range []string{"success", "resumed success", "old server", "wrong deployment", "application denied", "public denied", "edge health", "identified old deployment"} {
		t.Run(outcome, func(t *testing.T) {
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
				if outcome == "public denied" {
					w.WriteHeader(http.StatusForbidden)
				}
				_, _ = w.Write([]byte("public response"))
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
					http.NotFound(w, r)
				}
			})
			runner := startTestRunner(t, startTestSource(t), "")
			runner.session.AppID, runner.session.DeploymentID = startResponseAppID, startResponseDeploymentID
			runner.projectUpdates = outcome != "resumed success"
			runner.session.RequireDeploymentEvidence = outcome == "resumed success"
			code := runner.sendTestRequest("/ready?temporary=1", "")
			success := outcome == "success" || outcome == "resumed success"
			if (code == 0) != success || publicCalls.Load() != 1 {
				t.Fatalf("code=%d public calls=%d output=%s", code, publicCalls.Load(), stdout)
			}
			wantOwner := int32(1)
			if outcome == "public denied" || outcome == "edge health" || outcome == "identified old deployment" {
				wantOwner = 0
			}
			if ownerCalls.Load() != wantOwner {
				t.Fatalf("owner calls=%d want=%d", ownerCalls.Load(), wantOwner)
			}
			if success && (runner.lastRequest == nil || !runner.lastRequest.publicDeploymentCheck || runner.lastRequest.servedDeployment != startResponseDeploymentID || strings.Contains(stdout.String(), "Verified URL") || !strings.Contains(stdout.String(), "owner-authorized check")) {
				t.Fatalf("owner check claimed public deployment evidence: %s", stdout)
			}
		})
	}
}
