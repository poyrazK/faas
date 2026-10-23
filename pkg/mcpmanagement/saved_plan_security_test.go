// adr: 20261023184309
package mcpmanagement

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestSavedEnvironmentReviewRestartRedactionAndSnapshot(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(fmt.Sprint(rollback), func(t *testing.T) {
			f, b := newEnvironmentFixture(t)
			f.protected = true
			store, path, key := testStateStore(t)
			opts := environmentTestOptions(b, true)
			opts.planStore, opts.planNamespace = store, "environment-review"
			m := newHistoryEnvironment(t, b, opts)
			id := planHistoryEnvironment(t, m, rollback)
			p := m.environmentPlans[id]
			p.token, p.approval, p.PromotionToken = "PRIVATE_NATIVE_TOKEN", "PRIVATE_APPROVAL", "PRIVATE_PREVIEW"
			p.Changes = []api.ProjectEnvironmentPromotionChange{{WorkloadSlug: "alpha", SourceRevision: "original-commit", TargetRevision: "target-commit", SourceWorkloadConfigHash: strings.Repeat("a", 64), EnvironmentChanges: []api.ProjectEnvironmentConfigChange{{Key: "private", After: json.RawMessage(`{"value":"PRIVATE_CONFIG"}`)}}, SecretChanges: []api.ProjectEnvironmentConfigChange{{Key: "private", Before: json.RawMessage(`"PRIVATE_SECRET"`)}}}}
			before := savedEnvironmentReview(t, m, id)
			for _, secret := range []string{"PRIVATE_NATIVE_TOKEN", "PRIVATE_APPROVAL", "PRIVATE_PREVIEW", "PRIVATE_CONFIG", "PRIVATE_SECRET"} {
				if bytes.Contains(before.Plan, []byte(secret)) {
					t.Fatalf("saved environment review exposed %s", secret)
				}
			}
			if err := m.persistEnvironmentPlans(); err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			next, err := OpenStateStore(path, key)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = next.Close() })
			opts.planStore, opts.AllowWrites = next, false
			restored := newHistoryEnvironment(t, b, opts)
			after := savedEnvironmentReview(t, restored, id)
			if !bytes.Equal(before.Plan, after.Plan) {
				t.Fatal("restart replaced the original environment review")
			}
			// The returned snapshot owns its bytes, even while another operation
			// updates the private plan or its nested DTOs.
			restored.environmentPlans[id].Changes[0].SourceRevision = "changed"
			if bytes.Contains(after.Plan, []byte(`"source_revision":"changed"`)) {
				t.Fatal("review aliased mutable stored state")
			}
			restored.environmentPlans[id].ExpiresAt = time.Now().Add(-time.Second)
			if _, err := restored.getEnvironmentPlan(t.Context(), getEnvironmentActionArgs{Project: "shop", Environment: "production", PlanID: id}); err == nil {
				t.Fatal("expired environment review was exposed")
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.writes != 0 || f.reviewRequests != 0 || f.approvalPolls != 0 {
				t.Fatal("reopening review exercised approval or native writes")
			}
		})
	}
}

func TestSavedEnvironmentReviewConsentRechecked(t *testing.T) {
	for _, change := range []string{"source", "target", "project", "account", "source-consent", "target-consent", "membership", "expiry", "removed"} {
		t.Run(change, func(t *testing.T) {
			f, b := newEnvironmentFixture(t)
			m := newHistoryEnvironment(t, b, environmentTestOptions(b, false))
			id := planHistoryEnvironment(t, m, false)
			result, err := m.getEnvironmentPlan(t.Context(), getEnvironmentActionArgs{Project: "shop", Environment: "production", PlanID: id})
			if err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			switch change {
			case "source":
				f.stagingID = "replacement"
			case "target":
				f.productionID = "replacement"
			case "project":
				f.projectID = "replacement"
			case "account":
				f.account = "replacement"
			case "source-consent":
				delete(m.opts.Environments.Named, "shop/staging")
			case "target-consent":
				delete(m.opts.Environments.Named, "shop/production")
			case "membership":
				f.extra = true
			case "expiry":
				m.environmentPlans[id].ExpiresAt = time.Now().Add(-time.Second)
			case "removed":
				delete(m.environmentPlans, id)
			}
			f.mu.Unlock()
			if result.(*guardedRead).check(t.Context()) == nil {
				t.Fatal("changed environment authority released a private saved proposal")
			}
		})
	}
}

func TestSavedEnvironmentReviewTargetChangesDuringSourceCheck(t *testing.T) {
	f, _ := newEnvironmentFixture(t)
	var replace atomic.Bool
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := httptest.NewRecorder()
		f.serve(recorder, r)
		if r.URL.Path == "/v1/projects/shop/environments/staging" && replace.CompareAndSwap(true, false) {
			f.mu.Lock()
			f.productionID = "replacement"
			f.mu.Unlock()
		}
		for key, values := range recorder.Header() {
			w.Header()[key] = values
		}
		w.WriteHeader(recorder.Code)
		_, _ = w.Write(recorder.Body.Bytes())
	}))
	t.Cleanup(b.Close)
	m := newHistoryEnvironment(t, b, environmentTestOptions(b, false))
	id := planHistoryEnvironment(t, m, false)
	result, err := m.getEnvironmentPlan(t.Context(), getEnvironmentActionArgs{Project: "shop", Environment: "production", PlanID: id})
	if err != nil {
		t.Fatal(err)
	}
	replace.Store(true)
	if result.(*guardedRead).check(t.Context()) == nil {
		t.Fatal("target replacement during source lookup released a saved review")
	}
}

func TestSavedRegressionReviewDiagnosticConsent(t *testing.T) {
	for _, protocol := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(protocol, func(t *testing.T) {
			f, b, opts := newTriageFixture(t)
			cs, ctx := connectConfiguredTestServer(t, b, opts, protocol)
			original := callTool(t, ctx, cs, "gregale_plan_regression_action", triageArgs("dismiss"), false)
			wireID := planID(t, original)
			assertSavedReview(t, callTool(t, ctx, cs, "gregale_get_plan", getActionArgs{App: "alpha", PlanID: wireID}, false), wireID)
			_, m, err := newManagementServer(api.NewClient(b.URL, testToken), opts)
			if err != nil {
				t.Fatal(err)
			}
			value, err := m.planRegressionAction(t.Context(), regressionActionArgs{App: "alpha", DeploymentID: discoveryDeploymentID, Route: "GET /orders", Action: "dismiss", Reason: "review exact triage"})
			if err != nil {
				t.Fatal(err)
			}
			id := value.(releasePlan).ID
			review := savedAppReview(t, m, getActionArgs{App: "alpha", PlanID: id})
			if !bytes.Contains(review.Plan, []byte(f.row.Version)) || !bytes.Contains(review.Plan, []byte(`"action":"dismiss"`)) {
				t.Fatal("triage review lost its pinned observation or dismissal")
			}
			result, err := m.getPlan(t.Context(), getActionArgs{App: "alpha", PlanID: id})
			if err != nil {
				t.Fatal(err)
			}
			delete(m.opts.Environments.RequestReadApps, "alpha")
			if result.(*guardedRead).check(t.Context()) == nil {
				t.Fatal("revoked diagnostic consent released regression review")
			}
			if _, err := m.getPlan(t.Context(), getActionArgs{App: "alpha", PlanID: id}); err == nil {
				t.Fatal("regression review ignored diagnostic consent")
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.writes != 0 || f.reads != 2 {
				t.Fatal("reopening triage review refreshed evidence or mutated it")
			}
		})
	}
}

func TestSavedServiceAbortAndIntegerProjection(t *testing.T) {
	_, b := newEnvironmentFixture(t)
	m := newHistoryEnvironment(t, b, environmentTestOptions(b, false))
	p := &releasePlan{ID: strings.Repeat("a", 64), Action: "service_abort", App: "alpha", RolloutMode: api.ExecutionModeService, TargetDeploymentID: "candidate", PredecessorDeploymentID: "predecessor", WorkloadRevision: 9007199254740993, Reason: "review " + testToken, ServiceRecovery: &api.ServiceRolloutRecoveryReceipt{RequestID: "PRIVATE_RECOVERY_KEY"}, RolloutPolicy: &rolloutPolicy{ServiceReplicas: &api.ServiceReplicas{Min: 1, Max: 2, Desired: 2}, StopGracePeriodSeconds: 30}}
	projection, err := m.projectSavedAppPlan(p)
	if err != nil {
		t.Fatal(err)
	}
	view, err := m.savedReview(projection)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(view.Plan, []byte(`"workload_revision":9007199254740993`)) || !bytes.Contains(view.Plan, []byte(`"predecessor_deployment_id":"predecessor"`)) || !bytes.Contains(view.Plan, []byte(`"desired":2`)) {
		t.Fatal("snapshot lost exact baseline integers or abort policy")
	}
	if bytes.Contains(view.Plan, []byte(testToken)) || bytes.Contains(view.Plan, []byte("PRIVATE_RECOVERY_KEY")) {
		t.Fatal("saved review exposed credentials or private execution receipts")
	}
	p.RolloutPolicy.ServiceReplicas.Desired = 1
	if !bytes.Contains(view.Plan, []byte(`"desired":2`)) {
		t.Fatal("review snapshot aliased a mutable policy")
	}
	for _, id := range []string{"../unknown", strings.Repeat("f", 64)} {
		if _, err := m.getPlan(t.Context(), getActionArgs{App: "alpha", PlanID: id}); err == nil {
			t.Fatal("invalid/unknown app plan was exposed")
		}
		if _, err := m.getEnvironmentPlan(t.Context(), getEnvironmentActionArgs{Project: "shop", Environment: "production", PlanID: id}); err == nil {
			t.Fatal("invalid/unknown environment plan was exposed")
		}
	}
}
