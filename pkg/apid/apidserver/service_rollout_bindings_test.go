package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestServiceRolloutBindingWorkerAndExactAbort(t *testing.T) {
	e, app, predecessor, candidate := apidserver.PromotionFixture(t)
	ctx := t.Context()
	if err := (*e.Store()).SetDeploymentCanaryState(ctx, candidate.ID, "none", 0, 0, time.Now(), "rolling_out"); err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	if _, err := (*e.Store()).SetBindingReleasePolicy(ctx, (*e.Acct()).ID, app.ID, "default", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero}); err != nil {
		t.Fatal(err)
	}
	queued, err := (*e.Store()).BeginServiceRolloutCutover(ctx, candidate.ID)
	if !state.IsBindingReleaseRequired(err) || queued.ServiceRolloutHandoff.BindingsCheck == nil {
		t.Fatalf("queue: %+v %v", queued, err)
	}
	cursor := 0
	if err := (*e.S()).ServiceRolloutBindingSweep(ctx, &cursor); err != nil {
		t.Fatal(err)
	}
	blocked, _ := (*e.Store()).DeploymentByID(ctx, candidate.ID)
	if gate := blocked.ServiceRolloutHandoff.BindingsCheck; gate.Status != "blocked" || len(gate.Blockers) == 0 || gate.DeploymentID != candidate.ID {
		t.Fatalf("missing structured blocker: %+v", gate)
	}
	apidserver.AssertPromotionWeights(t, e, predecessor, candidate, 0)
	for _, d := range []state.Deployment{candidate, predecessor} {
		if instance, err := (*e.Store()).CreateInstanceWithMode(ctx, app.ID, d.ID, "running", 512, state.DefaultLocalNodeName, uuid.NewString(), "service"); err != nil {
			t.Fatal(err)
		} else {
			(*e.Store()).BackdateForTest(instance.ID, time.Now())
		}
	}
	apidserver.CompletePromotionProbe(t, e, app, candidate, apidserver.PassedPostgresVerification)
	if err := (*e.S()).ServiceRolloutBindingSweep(ctx, &cursor); err != nil {
		t.Fatal(err)
	}
	routed, _ := (*e.Store()).DeploymentByID(ctx, candidate.ID)
	if routed.ServiceRolloutHandoff.BindingsCheck.Status != "passed" || routed.ServiceRolloutHandoff.BindingsCheck.AuditID == "" {
		t.Fatalf("verified candidate blocked: %+v", routed.ServiceRolloutHandoff)
	}
	apidserver.AssertPromotionWeights(t, e, predecessor, candidate, 100)
	response := e.Do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/rollouts/recover", api.RecoverRolloutRequest{Action: "abort", DeploymentID: candidate.ID, ExpectedPredecessorDeploymentID: predecessor.ID, Reason: "stop service"}, map[string]string{"Idempotency-Key": "service-abort-exact"})
	var receipt api.RolloutTransitionResponse
	if response.Code != 202 || json.Unmarshal(response.Body.Bytes(), &receipt) != nil || receipt.ServiceRecovery == nil || receipt.ServiceRecovery.Status != "accepted" || receipt.Recovery != nil {
		t.Fatalf("async receipt: %d %s", response.Code, response.Body)
	}
	apidserver.AssertPromotionWeights(t, e, predecessor, candidate, 100)
	if err := (*e.S()).ServiceRolloutBindingSweep(ctx, &cursor); err != nil {
		t.Fatal(err)
	}
	apidserver.AssertPromotionWeights(t, e, predecessor, candidate, 100)
	apidserver.CompletePromotionProbe(t, e, app, predecessor, apidserver.PassedPostgresVerification)
	(*
	// A replacement APID reads the durable blocked request and resumes it
	// without resubmitting operator intent or choosing another predecessor.
	e.S()) = apidserver.NewServer((*e.Store()), (*(*e.S()).Log()), "gregale.dev", apidserver.NoopNotifier{}).
		WithManagedPostgres((*(*e.S()).ManagedPostgres()), nil, (*(*e.S()).ManagedPostgresBindings()), nil, nil, nil)
	cursor = 0
	if err := (*e.S()).ServiceRolloutBindingSweep(ctx, &cursor); err != nil {
		t.Fatal(err)
	}
	apidserver.AssertPromotionWeights(t, e, predecessor, candidate, 0)
	stillLive, _ := (*e.Store()).DeploymentByID(ctx, candidate.ID)
	if stillLive.Status != state.DeployLive || stillLive.RolloutState != "rolling_out" || stillLive.ServiceRolloutHandoff.Phase != "routing" {
		t.Fatalf("worker bypassed acknowledgement/drain: %+v", stillLive)
	}
}
