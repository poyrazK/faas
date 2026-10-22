package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestStartPublicPreviewRequiresAcceptedIdentitiesInEveryEnvironment(t *testing.T) {
	for _, scope := range []string{"default", "staging"} {
		for _, outcome := range []string{"matching", "wrong app", "wrong deployment", "missing app", "missing deployment", "expired", "empty URL", "unsupported"} {
			t.Run(scope+"/"+outcome, func(t *testing.T) {
				stdout, stderr := main.StartTestEnvironment(t)
				var previewCalls, canonicalCalls atomic.Int32
				serve := func(calls *atomic.Int32) http.HandlerFunc {
					return func(w http.ResponseWriter, r *http.Request) {
						calls.Add(1)
						if r.Header.Get("Authorization") != "" {
							t.Error("account credentials reached a public route")
						}
						w.Header().Set(api.DeploymentIDHeader, main.StartResponseDeploymentID)
						_, _ = w.Write([]byte("accepted deployment"))
					}
				}
				preview := httptest.NewServer(serve(&previewCalls))
				t.Cleanup(preview.Close)
				canonical := httptest.NewServer(serve(&canonicalCalls))
				t.Cleanup(canonical.Close)
				main.StartTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/v1/apps/first-app":
						_ = json.NewEncoder(w).Encode(api.AppResponse{ID: main.StartResponseAppID, CanonicalURL: canonical.URL})
					case "/v1/deployments/" + main.StartResponseDeploymentID + "/url":
						result := api.DeploymentPreviewURL{AppID: main.StartResponseAppID, DeploymentID: main.StartResponseDeploymentID, Alive: true, URL: preview.URL}
						switch outcome {
						case "wrong app":
							result.AppID = main.StartResponseInstanceID
						case "wrong deployment":
							result.DeploymentID = main.StartResponseInstanceID
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
				runner := main.StartTestRunner(t, main.StartTestSource(t), "")
				(*runner.Session()).AppID, (*runner.Session()).DeploymentID = main.StartResponseAppID, main.StartResponseDeploymentID
				if scope != "default" {
					(*runner.Session()).Environment = scope
				}
				code := runner.SendTestRequest("/", "")
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
				if code != 0 && ((*runner.LastRequest()) != nil || strings.Contains(stdout.String(), "Your app answered.")) {
					t.Fatal("invalid preview produced successful evidence")
				}
			})
		}
	}
}
