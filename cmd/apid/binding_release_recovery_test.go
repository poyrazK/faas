package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestBindingReleaseRecoveryOperatorAndWorker(t *testing.T) {
	for _, worker := range []bool{false, true} {
		t.Run(map[bool]string{false: "operator", true: "worker"}[worker], func(t *testing.T) {
			e, app, predecessor, candidate := promotionFixture(t)
			ctx := context.Background()
			if _, err := e.store.UpdateDeploymentTraffic(ctx, candidate.ID, 25); err != nil {
				t.Fatal(err)
			}
			if err := e.store.SetDeploymentCanaryState(ctx, candidate.ID, "balanced", 1, 4, time.Now().Add(-time.Minute), "rolling_out"); err != nil {
				t.Fatal(err)
			}
			zero := int64(0)
			if _, err := e.store.SetBindingReleasePolicy(ctx, e.acct.ID, app.ID, "default", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero}); err != nil {
				t.Fatal(err)
			}
			path := "/v1/apps/" + app.Slug + "/rollouts/recover"
			req := api.RecoverRolloutRequest{Action: "abort", DeploymentID: candidate.ID, ExpectedPredecessorDeploymentID: predecessor.ID, Reason: "broken release"}
			const token = "canary-recovery-action-token-0000000001"
			mux := http.NewServeMux()
			if err := e.s.mountInternalSafeDeploy(mux, "127.0.0.1:9101", "canary-recovery-progress-token-00000001", token); err != nil {
				t.Fatal(err)
			}
			call := func() *httptest.ResponseRecorder {
				if !worker {
					return e.do(t, http.MethodPost, path, req, map[string]string{"Idempotency-Key": "same-binding-recovery-pair"})
				}
				body, _ := json.Marshal(api.RecoverDeploymentRolloutRequest{Action: "abort", ExpectedPredecessorDeploymentID: predecessor.ID, Reason: req.Reason})
				r := httptest.NewRequest(http.MethodPost, "/v1/internal/safe-deploy/deployments/"+candidate.ID+"/rollouts/recover", strings.NewReader(string(body)))
				r.RemoteAddr = "127.0.0.1:10000"
				r.Header.Set("Authorization", "Bearer "+token)
				r.Header.Set("Idempotency-Key", "same-binding-recovery-pair")
				w := httptest.NewRecorder()
				mux.ServeHTTP(w, r)
				return w
			}
			response := call()
			var problem api.Problem
			if response.Code != 409 || json.Unmarshal(response.Body.Bytes(), &problem) != nil || problem.BindingsCheck == nil || problem.BindingsCheck.DeploymentID != predecessor.ID {
				t.Fatalf("missing predecessor evidence: %d %s", response.Code, response.Body.String())
			}
			assertPromotionWeights(t, e, predecessor, candidate, 25)
			completePromotionProbe(t, e, app, candidate, passedPostgresVerification)
			if response = call(); response.Code != 409 {
				t.Fatalf("candidate evidence bypass: %d %s", response.Code, response.Body.String())
			}
			assertPromotionWeights(t, e, predecessor, candidate, 25)
			completePromotionProbe(t, e, app, predecessor, passedPostgresVerification)
			response = call()
			var receipt api.RolloutTransitionResponse
			if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &receipt) != nil || receipt.Recovery == nil || receipt.Recovery.PredecessorDeploymentID != predecessor.ID || len(receipt.Recovery.BindingsChecks) != 1 || !receipt.Recovery.BindingsChecks[0].Passed || receipt.AuditID == "" || receipt.Deployment.RolloutState != "aborted" {
				t.Fatalf("verified recovery: %d %s", response.Code, response.Body.String())
			}
			assertPromotionWeights(t, e, predecessor, candidate, 0)
			replayed := call()
			if replayed.Code != 200 || replayed.Header().Get("Idempotent-Replayed") != "true" || replayed.Body.String() != response.Body.String() {
				t.Fatalf("successful exact recovery was not replayed: %d %s", replayed.Code, replayed.Body.String())
			}
			p, err := e.store.GetBindingReleasePolicy(ctx, e.acct.ID, app.ID, "default")
			if err != nil || p.Mode != "enforce" {
				t.Fatalf("recovery disabled enforcement: %+v %v", p, err)
			}
		})
	}
}

func TestBindingReleaseRecoverySelectorsAndPermissions(t *testing.T) {
	e, app, predecessor, candidate := promotionFixture(t)
	ctx := context.Background()
	if _, err := e.store.UpdateDeploymentTraffic(ctx, candidate.ID, 25); err != nil {
		t.Fatal(err)
	}
	if err := e.store.SetDeploymentCanaryState(ctx, candidate.ID, "balanced", 1, 4, time.Now(), "rolling_out"); err != nil {
		t.Fatal(err)
	}
	path := "/v1/apps/" + app.Slug + "/rollouts/recover"
	for _, req := range []api.RecoverRolloutRequest{
		{Action: "abort", DeploymentID: candidate.ID},
		{Action: "abort", ExpectedPredecessorDeploymentID: predecessor.ID},
		{Action: "promote", DeploymentID: candidate.ID, ExpectedPredecessorDeploymentID: predecessor.ID},
		{Action: "abort", DeploymentID: candidate.ID, ExpectedPredecessorDeploymentID: candidate.ID},
	} {
		if response := e.do(t, http.MethodPost, path, req, nil); response.Code != 422 {
			t.Fatalf("invalid selectors: %d %s", response.Code, response.Body.String())
		}
		assertPromotionWeights(t, e, predecessor, candidate, 25)
	}
	zero := int64(0)
	if _, err := e.store.SetBindingReleasePolicy(ctx, e.acct.ID, app.ID, "default", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero}); err != nil {
		t.Fatal(err)
	}
	completePromotionProbe(t, e, app, predecessor, passedPostgresVerification)
	pt, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateAPIKey(ctx, e.acct.ID, hash, "write-only", []string{api.ScopeAppsRead, api.ScopeDeployWrite}); err != nil {
		t.Fatal(err)
	}
	e.key = pt
	req := api.RecoverRolloutRequest{Action: "abort", DeploymentID: candidate.ID, ExpectedPredecessorDeploymentID: predecessor.ID}
	response := e.do(t, http.MethodPost, path, req, map[string]string{"X-Binding-Release-Worker": "true"})
	if response.Code != 409 {
		t.Fatalf("public write-only key gained inventory reads: %d %s", response.Code, response.Body.String())
	}
	assertPromotionWeights(t, e, predecessor, candidate, 25)
	// A policy opt-out remains explicit for legacy app-selected recovery.
	response = e.do(t, http.MethodPost, path, api.RecoverRolloutRequest{Action: "abort"}, nil)
	if response.Code != 409 {
		t.Fatalf("legacy bypass: %d %s", response.Code, response.Body.String())
	}
}
