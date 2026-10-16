//go:build metal

package e2e_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// This extends the same opt-in, exclusive-host journey. It must never run on
// the shared nested diagnostic node or substitute for the native leak gate.
func (j *nativeGitOpsJourney) runWorkloadRetirement(t *testing.T) {
	t.Helper()
	t.Log("GitOps native retirement: retain accepted work and restart the controller")
	current, err := j.client.GetEnvironmentGitOps(t.Context(), j.project.Slug, "production")
	if err != nil {
		t.Fatal(err)
	}
	prune := true
	if _, err := j.client.UpdateEnvironmentGitSource(t.Context(), j.project.Slug, "production", api.EnvironmentGitSourceUpdate{ExpectedGeneration: current.Source.Generation, Prune: &prune}); err != nil {
		t.Fatal(err)
	}
	// Keep a genuine accepted queue identity outside the dispatch window. Its
	// message, original binding and attempt count must survive both cutovers.
	retained, err := j.store.EnqueueInvocation(t.Context(), state.Invocation{AccountID: j.source.AccountID, AppID: j.worker.ID,
		Source: state.InvocationQueue, DeploymentScope: "production", QueueBindingID: j.bindingID, QueueName: "orders",
		Method: http.MethodPost, Path: "/", Payload: json.RawMessage(`{"id":"retained-through-workload-retirement"}`), DueAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	inFlight := j.sendQueue(t, 10000)
	j.waitQueue(t, inFlight, true)
	definition := j.retirementDefinition(t)
	definition.WorkloadPruningPolicy = "retain"
	delete(definition.Workloads, "worker")
	removedSHA := strings.Repeat("d", 40)
	j.provider.add(t, removedSHA, definition)
	removed := j.approve(t, removedSHA)
	j.waitStatus(t, "retirement reservation", 4*time.Minute, func(s api.EnvironmentGitOpsStatusResponse) bool {
		for _, r := range s.WorkloadRetirements {
			if r.Resource == "workload/worker" && r.Generation == removed.Source.Generation {
				return true
			}
		}
		return false
	})
	if err := j.h.RestartAPID(); err != nil {
		t.Fatal(err)
	}
	retired := j.waitStatus(t, "retired worker", 6*time.Minute, func(s api.EnvironmentGitOpsStatusResponse) bool {
		for _, r := range s.WorkloadRetirements {
			if r.Resource == "workload/worker" && r.Generation == removed.Source.Generation && r.Phase == "retired" {
				return true
			}
		}
		return false
	})
	j.assertGatewayBody(t, helloBody)
	originalID := ""
	for _, r := range retired.WorkloadRetirements {
		if r.Resource == "workload/worker" {
			originalID = r.ID
			if r.AppID != j.worker.ID || len(r.QueueBindingIDs) != 1 || r.QueueBindingIDs[0] != j.bindingID {
				t.Fatal("retirement replaced original workload/queue identity")
			}
		}
	}
	j.assertRetainedRetirementMessage(t, retained)
	body, code := doReq(t, j.h, j.key, http.MethodPost, "/v1/apps/"+j.worker.Slug+"/queues/send", api.QueueSendRequest{Environment: "production", QueueName: "orders", Payload: json.RawMessage(`{"id":"must-be-refused"}`)})
	if code >= 200 && code < 300 {
		t.Fatalf("retired workload accepted new work: status=%d body=%s", code, body)
	}
	t.Log("GitOps native retirement: reviewed original-identity recovery requires fresh qualification")
	recovery := j.retirementDefinition(t)
	recovery.WorkloadPruningPolicy = "retain"
	recovery.WorkloadRecoveries = map[string]string{"worker": j.worker.ID}
	worker := recovery.Workloads["worker"]
	worker.QueueRecoveries = map[string]string{"orders": j.bindingID}
	recovery.Workloads["worker"] = worker
	recoverySHA := strings.Repeat("e", 40)
	j.provider.add(t, recoverySHA, recovery)
	approved := j.approve(t, recoverySHA)
	activated := j.waitActivated(t, approved.Revision.ID, approved.Source.Generation)
	if activated.WorkloadEvidence == nil || !activated.WorkloadEvidence.Qualified {
		t.Fatal("recovery activated without fresh qualification")
	}
	found := false
	for _, r := range activated.WorkloadRetirements {
		if r.Resource == "workload/worker" {
			found = r.ID == originalID && r.Phase == "recovered" && r.RecoveryRevisionID == approved.Revision.ID && r.RecoveryGeneration == approved.Source.Generation
		}
	}
	if !found {
		t.Fatal("recovery lost its retained original identity or current generation")
	}
	j.waitQueue(t, j.sendQueue(t, 0), false)
	j.waitApplied(t, approved.Revision.ID, approved.Source.Generation)
	j.assertRetainedRetirementMessage(t, retained)
	j.assertGatewayBody(t, helloBody)
}
func (j *nativeGitOpsJourney) retirementDefinition(t *testing.T) api.EnvironmentDefinition {
	t.Helper()
	raw, err := json.Marshal(j.original)
	if err != nil {
		t.Fatal(err)
	}
	var definition api.EnvironmentDefinition
	if err := json.Unmarshal(raw, &definition); err != nil {
		t.Fatal(err)
	}
	return definition
}
func (j *nativeGitOpsJourney) assertRetainedRetirementMessage(t *testing.T, original state.Invocation) {
	t.Helper()
	current, err := j.store.InvocationByID(t.Context(), original.ID)
	if err != nil || current.ID != original.ID || current.QueueBindingID != original.QueueBindingID || current.AppID != original.AppID || current.DeploymentScope != original.DeploymentScope || current.State != state.InvocationPending || current.Attempts != original.Attempts || string(current.Payload) != string(original.Payload) {
		t.Fatalf("retained pending work changed through retirement/recovery: original=%s current=%+v err=%v", original.ID, current, err)
	}
}
