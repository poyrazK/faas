package pgintegration_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/bindings"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// These owner-API fixtures exercise PostgreSQL control-plane authority;
// they are not VM qualification evidence.
func TestManagedWorkerQueueCanaryUsesActiveRelease(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	basic := state.NewPgStore(pool)
	canaries := basic
	eligibility := basic
	lease, plan, queue := workloadGraphFixtureWithWorkerVariables(t, basic, true, api.ServiceBindingTransportHTTPS, map[string]string{api.QueueCanaryCapabilityEnv: `["orders"]`})
	lease, plan, requests := prepareQualificationFixture(t, basic, lease, plan)
	placement := qualificationPlacement(t, basic, 4096)
	requests, err := basic.ClaimEnvironmentWorkloadQualificationGraphForNode(t.Context(), requests[0].GraphID, placement.NodeID, "worker-canary-fixture", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var worker state.EnvironmentWorkloadQualificationRequest
	for _, request := range requests {
		placement.WakeID = uuid.NewString()
		completeClaimedQualificationReceipts(t, basic, request, placement)
		if request.Resource == "workload/worker" {
			worker = request
		}
	}
	release, active, err := basic.ActivateEnvironmentGitOpsWorkloadGraph(t.Context(), lease, plan)
	if err != nil || !active {
		t.Fatalf("activation: %v", err)
	}
	dep, err := basic.DeploymentByID(t.Context(), worker.DeploymentID)
	if err != nil || dep.TrafficPercent != 0 {
		t.Fatalf("worker must have zero HTTP traffic: %+v %v", dep, err)
	}
	// This explicitly reviewed variable, not mutable app env or an HTTP pin,
	// declares the handler capability.
	if state.QueueCanaryDeploymentSupports(dep, "orders") {
		t.Fatal("fixture unexpectedly inherited a deployment capability")
	}
	allowed, reason, err := eligibility.QueueBindingCanaryEligibility(t.Context(), lease.Source.AccountID, worker.AppID, dep.ID, "orders")
	if err != nil || !allowed || reason != "" {
		t.Fatalf("eligibility: %v %s %v", allowed, reason, err)
	}
	inventory := api.AppBindingInventory{Complete: true, VerificationDeploymentID: dep.ID, VerificationScope: dep.Scope, Bindings: []api.AppBindingInventoryItem{{Type: api.BindingTypeQueue, Binding: "orders", Scope: dep.Scope}}}
	bindings.ApplyQueueCanaryInventory(t.Context(), basic, lease.Source.AccountID, worker.AppID, &inventory)
	if !inventory.Complete || !inventory.Bindings[0].QueueCanarySupported || inventory.Bindings[0].QueueCanaryReason != "" {
		t.Fatalf("inventory admission disagreement: %+v", inventory)
	}
	req := api.CreateQueueBindingCanaryRequest{DeploymentID: dep.ID, Binding: "orders", TTLSeconds: 60, PublishConsent: true}
	denied := req
	denied.PublishConsent = false
	if _, err := canaries.CreateQueueBindingCanary(t.Context(), lease.Source.AccountID, worker.AppID, denied); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("consent: %v", err)
	}
	// A publisher takes the release lock before the app lock. Admission must
	// observe the still-active committed graph without waiting in reverse order.
	publisher, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = publisher.Rollback(t.Context()) }()
	if _, err := publisher.Exec(t.Context(), "SELECT id FROM project_release_sets WHERE id=$1 FOR UPDATE", release.ID); err != nil {
		t.Fatal(err)
	}
	bounded, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	r, err := canaries.CreateQueueBindingCanary(bounded, lease.Source.AccountID, worker.AppID, req)
	cancel()
	if err != nil {
		t.Fatalf("canary inverted publisher lock order: %v", err)
	}
	if err := publisher.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err != nil || r.ReleaseID != release.ID {
		t.Fatalf("publication: %+v %v", r, err)
	}
	trigger := queue.Changes[0].TriggerID
	claimed, err := basic.ClaimQueueTriggerInvocation(t.Context(), r.ID, trigger, worker.AppID, "orders", 30)
	if err != nil {
		t.Fatal(err)
	}
	var headers map[string]string
	if json.Unmarshal(claimed.Headers, &headers) != nil || headers[api.ReleaseHeader] != release.ID || headers[api.RevisionHeader] != "" || claimed.RetryPolicy().MaxAttempts != 1 {
		t.Fatalf("frozen invocation: %+v", claimed)
	}
	_, version, err := state.ResolveInvocationVersion(t.Context(), basic, claimed)
	if err != nil || version.ReleaseID != release.ID || version.DeploymentID != dep.ID {
		t.Fatalf("dispatch: %+v %v", version, err)
	}
	transport := claimed
	transport.Source = state.InvocationSource("esm")
	transport.QueueName = ""
	prepared, transportVersion, err := state.ResolveInvocationVersion(t.Context(), basic, transport)
	if err != nil || prepared.Source != state.InvocationQueue || prepared.QueueName != "orders" || transportVersion != version {
		t.Fatalf("ESM canary lost durable queue authority: %+v %v", transportVersion, err)
	}
	tampered := claimed
	tampered.Headers, _ = json.Marshal(map[string]string{api.ReleaseHeader: uuid.NewString()})
	if _, _, err := state.ResolveInvocationVersion(t.Context(), basic, tampered); err == nil {
		t.Fatal("changed release dispatched")
	}
	for _, field := range []string{"binding", "queue", "scope", "source"} {
		changed := claimed
		switch field {
		case "binding":
			changed.QueueBindingID = uuid.NewString()
		case "queue":
			changed.QueueName = "other"
		case "scope":
			changed.DeploymentScope = "staging"
		case "source":
			changed.Source = state.InvocationSource("workflow")
		}
		if _, _, err := state.ResolveInvocationVersion(t.Context(), basic, changed); err == nil {
			t.Fatalf("changed %s envelope dispatched", field)
		}
	}
	ack := state.EnvironmentWorkloadQueueServingAcknowledgement{AppID: worker.AppID, BindingID: queue.Binding.ID, TriggerID: trigger, InvocationID: r.ID, DeploymentID: dep.ID, Mode: "push", Scope: dep.Scope}
	ids, err := basic.CompleteEnvironmentQueueDeliveryClaims(t.Context(), worker.AppID, trigger, "queue", []string{r.ID}, []int32{1}, []int64{0}, []state.EnvironmentWorkloadQueueServingAcknowledgement{ack})
	if err != nil || len(ids) != 1 {
		t.Fatalf("completion: %v %v", ids, err)
	}
	got, err := canaries.GetQueueBindingCanary(t.Context(), lease.Source.AccountID, worker.AppID, r.ID)
	if err != nil || got.Status != "succeeded" || got.ReleaseID != release.ID {
		t.Fatalf("evidence: %+v %v", got, err)
	}
	next, err := canaries.CreateQueueBindingCanary(t.Context(), lease.Source.AccountID, worker.AppID, req)
	if err != nil {
		t.Fatal(err)
	}
	inflight, err := basic.ClaimQueueTriggerInvocation(t.Context(), next.ID, trigger, worker.AppID, "orders", 30)
	if err != nil {
		t.Fatal(err)
	}
	// A policy revision revokes both completed proof and in-flight authority.
	policies := basic
	policy, err := policies.GetBindingReleasePolicy(t.Context(), lease.Source.AccountID, worker.AppID, dep.Scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := policies.SetBindingReleasePolicy(t.Context(), lease.Source.AccountID, worker.AppID, dep.Scope, api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &policy.Revision}); err != nil {
		t.Fatal(err)
	}
	got, err = canaries.GetQueueBindingCanary(t.Context(), lease.Source.AccountID, worker.AppID, r.ID)
	if err != nil || got.Status != "blocked" {
		t.Fatalf("policy retained proof: %+v %v", got, err)
	}
	if _, _, err := state.ResolveInvocationVersion(t.Context(), basic, inflight); err == nil {
		t.Fatal("policy retained dispatch")
	}
	if got, err := canaries.CancelQueueBindingCanary(t.Context(), lease.Source.AccountID, worker.AppID, next.ID); err != nil || got.Status != "cancelled" {
		t.Fatalf("cancel stale: %+v %v", got, err)
	}
	source := basic
	suspended := true
	if _, err := source.UpdateEnvironmentGitSource(t.Context(), lease.Source.AccountID, lease.Source.ID, state.EnvironmentGitSourceUpdate{ExpectedGeneration: lease.Source.Generation, Suspended: &suspended}); err != nil {
		t.Fatal(err)
	}
	allowed, reason, err = eligibility.QueueBindingCanaryEligibility(t.Context(), lease.Source.AccountID, worker.AppID, dep.ID, "orders")
	if err != nil || allowed || reason != "queue_canary_context_unavailable" {
		t.Fatalf("suspended eligibility: %v %s %v", allowed, reason, err)
	}
	if _, err := canaries.CreateQueueBindingCanary(t.Context(), lease.Source.AccountID, worker.AppID, req); err == nil {
		t.Fatal("suspended source admitted canary")
	}
}
