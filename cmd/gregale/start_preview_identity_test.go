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

func TestStartPublicPreviewRequiresAcceptedIdentitiesInEveryEnvironment(t *testing.T) {
	for _, scope := range []string{"default", "staging"} {
		for _, outcome := range []string{"matching", "wrong app", "wrong deployment", "missing app", "missing deployment", "expired", "empty URL", "unsupported"} {
			t.Run(scope+"/"+outcome, func(t *testing.T) {
				stdout, stderr := startTestEnvironment(t)
				var previewCalls, canonicalCalls atomic.Int32
				serve := func(calls *atomic.Int32) http.HandlerFunc {
					return func(w http.ResponseWriter, r *http.Request) {
						calls.Add(1)
						if r.Header.Get("Authorization") != "" {
							t.Error("account credentials reached a public route")
						}
						w.Header().Set(api.DeploymentIDHeader, startResponseDeploymentID)
						_, _ = w.Write([]byte("accepted deployment"))
					}
				}
				preview := httptest.NewServer(serve(&previewCalls))
				t.Cleanup(preview.Close)
				canonical := httptest.NewServer(serve(&canonicalCalls))
				t.Cleanup(canonical.Close)
				startTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/v1/apps/first-app":
						_ = json.NewEncoder(w).Encode(api.AppResponse{ID: startResponseAppID, CanonicalURL: canonical.URL})
					case "/v1/deployments/" + startResponseDeploymentID + "/url":
						result := api.DeploymentPreviewURL{AppID: startResponseAppID, DeploymentID: startResponseDeploymentID, Alive: true, URL: preview.URL}
						switch outcome {
						case "wrong app":
							result.AppID = startResponseInstanceID
						case "wrong deployment":
							result.DeploymentID = startResponseInstanceID
						case "missing app":
							result.AppID = ""
						case "missing deployment":
							result.DeploymentID = ""
						case "expired":
							result.Alive = false
						case "empty URL":
							result.URL = ""
						case "unsupported":
							http.NotFound(w, r)
							return
						}
						_ = json.NewEncoder(w).Encode(result)
					default:
						t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
						http.NotFound(w, r)
					}
				})
				runner := startTestRunner(t, startTestSource(t), "")
				runner.session.AppID, runner.session.DeploymentID = startResponseAppID, startResponseDeploymentID
				if scope != "default" {
					runner.session.Environment = scope
				}
				code := runner.sendTestRequest("/", "")
				fallback := scope == "default" && (outcome == "expired" || outcome == "empty URL" || outcome == "unsupported")
				if success := outcome == "matching" || fallback; (code == 0) != success {
					t.Fatalf("code=%d output=%s stderr=%s", code, stdout, stderr())
				}
				wantPreview, wantCanonical := int32(0), int32(0)
				if outcome == "matching" {
					wantPreview = 1
				}
				if fallback {
					wantCanonical = 1
				}
				if previewCalls.Load() != wantPreview || canonicalCalls.Load() != wantCanonical {
					t.Fatalf("checked an unverified target: preview=%d canonical=%d", previewCalls.Load(), canonicalCalls.Load())
				}
				if code != 0 && (runner.lastRequest != nil || strings.Contains(stdout.String(), "Your app answered.")) {
					t.Fatal("invalid preview produced successful evidence")
				}
			})
		}
	}
}
