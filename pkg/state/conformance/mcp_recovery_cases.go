package conformance

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

// Compatibility snapshots must be isolated from caller mutation, and recovery
// leases must fence stale completions across both store implementations.
func testMCPCompatibilityAndRecovery(t *testing.T, fx *Fixture) {
	s, ctx := fx.Store, fx.Ctx
	if _, err := s.GetMCPTaskCompatibilityReport(ctx, fx.App.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing compatibility snapshot: %v", err)
	}
	observed := time.Now().UTC().Truncate(time.Microsecond)
	handlers := []state.MCPTaskHandlerCompatibility{{ToolName: "checkout", HandlerVersion: "v1", WaitingTasks: 2, LiveCompatibleWorkers: 1, Deployments: []state.MCPTaskHandlerDeploymentWorkers{{DeploymentID: fx.Deployment.ID, Workers: 1}}}}
	expected := state.MCPTaskCompatibilityReport{Handlers: handlers, ObservedAt: observed}
	if err := s.PutMCPTaskCompatibilityReport(ctx, fx.App.ID, expected); err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.GetMCPTaskCompatibilityReport(ctx, fx.App.ID)
	if err != nil || snapshot.Truncated || !snapshot.ObservedAt.Equal(observed) || !reflect.DeepEqual(snapshot.Handlers, handlers) {
		t.Fatalf("compatibility round trip: %+v %v", snapshot, err)
	}
	snapshot.Handlers[0].Deployments[0].Workers = 99
	snapshot, err = s.GetMCPTaskCompatibilityReport(ctx, fx.App.ID)
	if err != nil || snapshot.Handlers[0].Deployments[0].Workers != 1 {
		t.Fatalf("caller mutation escaped: %+v %v", snapshot, err)
	}
	if err := s.PutMCPTaskCompatibilityReport(ctx, fx.App.ID, state.MCPTaskCompatibilityReport{ObservedAt: observed}); err == nil {
		t.Fatal("nil handlers accepted")
	}
	if err := s.PutMCPTaskCompatibilityReport(ctx, fx.App.ID, state.MCPTaskCompatibilityReport{Handlers: []state.MCPTaskHandlerCompatibility{}, Truncated: true, ObservedAt: observed}); err != nil {
		t.Fatal(err)
	}
	snapshot, err = s.GetMCPTaskCompatibilityReport(ctx, fx.App.ID)
	if err != nil || snapshot.Handlers == nil || len(snapshot.Handlers) != 0 || !snapshot.Truncated {
		t.Fatalf("empty replacement: %+v %v", snapshot, err)
	}
	candidate, err := s.CreateDeployment(ctx, state.Deployment{AppID: fx.App.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:mcp-conformance", Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]string{"deployment_id": candidate.ID})
	if err != nil {
		t.Fatal(err)
	}
	samePayload := func(value json.RawMessage) bool {
		var fields map[string]string
		return json.Unmarshal(value, &fields) == nil && len(fields) == 1 && fields["deployment_id"] == candidate.ID
	}
	recovery := state.MCPTaskPromotionRecovery{Channel: "deployment_ready", Payload: payload, NodeID: fx.Node.ID}
	if err := s.RecordMCPTaskPromotionHold(ctx, candidate.ID, "waiting for compatible workers", recovery); err != nil {
		t.Fatal(err)
	}
	hold, err := s.MCPTaskPromotionHold(ctx, candidate.ID)
	if err != nil || hold == nil || hold.Detail != "waiting for compatible workers" || hold.RecoveryAttempts != 0 {
		t.Fatalf("promotion hold: %+v %v", hold, err)
	}
	age, err := s.MCPTaskPromotionHoldAgeSeconds(ctx, fx.Account.ID, fx.App.ID, hold.HeldAt.Add(10*time.Second))
	if err != nil || age < 9.99 || age > 10.01 {
		t.Fatalf("hold age: %f %v", age, err)
	}
	if age, err := s.MCPTaskPromotionHoldAgeSeconds(ctx, uuid.NewString(), fx.App.ID, time.Now().Add(time.Hour)); err != nil || age != 0 {
		t.Fatalf("cross-account hold age: %f %v", age, err)
	}
	if changed, err := s.ReconcileObsoleteMCPTaskHolds(ctx, fx.Node.ID); err != nil || len(changed) != 0 {
		t.Fatalf("current candidate superseded: %+v %v", changed, err)
	}
	if work, err := s.ClaimMCPTaskPromotionRecovery(ctx, "another-node"); err != nil || work != nil {
		t.Fatalf("cross-node claim: %+v %v", work, err)
	}
	work, err := s.ClaimMCPTaskPromotionRecovery(ctx, fx.Node.ID)
	if err != nil || work == nil || work.DeploymentID != candidate.ID || work.Generation != 1 || work.Attempts != 1 || !samePayload(work.Payload) {
		t.Fatalf("first recovery lease: %+v %v", work, err)
	}
	if duplicate, err := s.ClaimMCPTaskPromotionRecovery(ctx, fx.Node.ID); err != nil || duplicate != nil {
		t.Fatalf("duplicate recovery lease: %+v %v", duplicate, err)
	}
	if completed, err := s.FinishMCPTaskPromotionRecovery(ctx, candidate.ID, work.Generation+1, "recovered"); err != nil || completed {
		t.Fatalf("stale completion: %t %v", completed, err)
	}
	if err := s.RetryMCPTaskPromotionRecovery(ctx, candidate.ID, "conformance-operator"); err != nil {
		t.Fatal(err)
	}
	if completed, err := s.FinishMCPTaskPromotionRecovery(ctx, candidate.ID, work.Generation, "held"); err != nil || !completed {
		t.Fatalf("finish held lease: %t %v", completed, err)
	}
	if err := s.RetryMCPTaskPromotionRecovery(ctx, candidate.ID, "conformance-operator"); err != nil {
		t.Fatal(err)
	}
	second, err := s.ClaimMCPTaskPromotionRecovery(ctx, fx.Node.ID)
	if err != nil || second == nil || second.Generation != 2 || second.Attempts != 2 || !samePayload(second.Payload) {
		t.Fatalf("retry lease: %+v %v", second, err)
	}
	if err := s.PutMCPTaskCompatibilityReport(ctx, fx.App.ID, state.MCPTaskCompatibilityReport{Handlers: []state.MCPTaskHandlerCompatibility{}, ObservedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkMCPTaskRecoveryDeploymentLive(ctx, candidate.ID, true); err != nil {
		t.Fatal(err)
	}
	if completed, err := s.FinishMCPTaskPromotionRecovery(ctx, candidate.ID, second.Generation, "recovered"); err != nil || !completed {
		t.Fatalf("finish recovered lease: %t %v", completed, err)
	}
	if hold, err := s.MCPTaskPromotionHold(ctx, candidate.ID); err != nil || hold != nil {
		t.Fatalf("live deployment retained hold: %+v %v", hold, err)
	}
	if age, err := s.MCPTaskPromotionHoldAgeSeconds(ctx, fx.Account.ID, fx.App.ID, time.Now().Add(time.Hour)); err != nil || age != 0 {
		t.Fatalf("live deployment retained hold age: %f %v", age, err)
	}
	if status, err := s.DeploymentWorkerDrainStatus(ctx, candidate.ID); err != nil || status != nil {
		t.Fatalf("invented drain evidence: %+v %v", status, err)
	}
	instance := uuid.NewString()
	for _, outcome := range []string{"draining", "completed"} {
		data, err := json.Marshal(map[string]string{"instance_id": instance, "outcome": outcome})
		if err != nil {
			t.Fatal(err)
		}
		kind := state.DeployWorkerDrainStarted
		if outcome == "completed" {
			kind = state.DeployWorkerDrainCompleted
		}
		if _, err := s.AppendDeploymentAudit(ctx, state.DeploymentAudit{DeploymentID: uuid.MustParse(candidate.ID), Kind: kind, Actor: "conformance", At: time.Now().UTC(), Data: data}); err != nil {
			t.Fatal(err)
		}
	}
	status, err := s.DeploymentWorkerDrainStatus(ctx, candidate.ID)
	if err != nil || status == nil || status.Completed != 1 || status.Active != 0 || status.Overdue != 0 || status.TimedOut != 0 || status.Failed != 0 {
		t.Fatalf("latest drain evidence: %+v %v", status, err)
	}
	canonical, err := s.DeploymentWorkerDrainStatus(ctx, uuid.MustParse(candidate.ID).String())
	if err != nil || !reflect.DeepEqual(canonical, status) {
		t.Fatalf("canonical drain identity: %+v %v", canonical, err)
	}
}
