package tasktoolrelease_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func handoverFixture(t *testing.T) (api.TaskToolReleaseIntentRecord, api.TaskToolReleaseIntentDecision, time.Time) {
	t.Helper()
	r, now := runningReceiptFixture(t)
	e := r.Execution
	e.FenceVersion = 1
	e.ClaimSHA256 = toolpublication.Digest([]byte(strings.Repeat("1", 64)))
	receipt := &api.TaskToolReleaseReceipt{Revision: 9, ReleaseID: tasktoolrelease.ExecutionReleaseID(r), Stage: "web_promoted", ServingCaptured: true,
		WebDeploymentID: uuid.NewString(), WorkerDeploymentID: uuid.NewString(), Promoted: true, PreviousDeployments: map[string]string{}, WorkerIDs: []string{uuid.NewString()}, Parked: map[string]bool{}}
	for _, pin := range r.Intent.Apps {
		if pin.Role == "web" {
			receipt.ServingDeploymentID = pin.Review.ServingDeploymentIDs[0]
		}
		if pin.Role == "previous_worker" {
			receipt.PreviousDeployments[pin.App] = pin.Review.LatestDeploymentID
		}
	}
	e.Receipt = receipt
	in := api.TaskToolReleaseIntentDecision{IntentSHA256: r.IntentSHA256, ReleaseFingerprint: r.Intent.ReleaseFingerprint, AuthorizerKeyID: uuid.NewString(), Decision: "recovery_stop",
		Recovery: &api.TaskToolRecoveryRequest{HandoverID: strings.Repeat("2", 64), OperationID: e.OperationID, Revision: receipt.Revision}}
	return r, in, now
}
func exitRequest(r api.TaskToolReleaseIntentRecord) api.TaskToolReleaseIntentDecision {
	return api.TaskToolReleaseIntentDecision{ReleaseFingerprint: r.Intent.ReleaseFingerprint, Execution: &api.TaskToolReleaseExecutionRequest{Action: "executor_exited", OperationID: r.Execution.OperationID, BindingsSHA256: r.Execution.BindingsSHA256, ClaimToken: strings.Repeat("1", 64)}}
}
func qualification(r api.TaskToolReleaseIntentRecord, now time.Time) api.TaskToolRecoveryQualification {
	e, h, receipt := r.Execution, r.Execution.Recovery, r.Execution.Receipt
	apps := append([]api.TaskToolReleaseApp{}, r.Intent.Apps...)
	for i := range apps {
		if apps[i].Role == "web" {
			apps[i].Review.LatestDeploymentID = receipt.WebDeploymentID
			apps[i].Review.ServingDeploymentIDs = []string{receipt.WebDeploymentID}
		}
		if apps[i].Role == "worker" {
			apps[i].Review.LatestDeploymentID = receipt.WorkerDeploymentID
		}
	}
	return api.TaskToolRecoveryQualification{HandoverID: h.ID, OperationID: e.OperationID, Revision: receipt.Revision, Action: "restore", TargetDeploymentID: receipt.ServingDeploymentID,
		ExpectedServingDeploymentID: receipt.WebDeploymentID, BindingsSHA256: e.BindingsSHA256, EvidenceSHA256: strings.Repeat("3", 64), NativeApps: apps, ObservedAt: now, ExpiresAt: now.Add(api.TaskToolRecoveryApprovalTTL)}
}
func TestMCPTaskRecoveryHandoverFencesAndSeparatesAuthority(t *testing.T) {
	r, in, now := handoverFixture(t)
	var err error
	stopped, err := tasktoolrelease.Recovery(r, in, false, now) // cancellation remains possible after app drift
	if err != nil || stopped.Execution.Recovery.State != "stopping" || r.Execution.Recovery != nil {
		t.Fatal("stop failed or mutated input", err)
	}
	retry, err := tasktoolrelease.Recovery(stopped, in, false, now.Add(time.Second))
	if err != nil || !retry.Execution.Recovery.RequestedAt.Equal(now) {
		t.Fatal("stop retry renewed handover", err)
	}
	request := exitRequest(stopped)
	request.Execution.Action = "guard"
	if _, err = tasktoolrelease.Execution(stopped, request, true, now); err == nil {
		t.Fatal("stopped executor guarded a later effect")
	}
	request.Execution.Action = "complete"
	if _, err = tasktoolrelease.Execution(stopped, request, true, now); err == nil {
		t.Fatal("stopped executor completed")
	}
	in.Decision = "recovery_qualify"
	q := api.TaskToolRecoveryQualification{}
	in.Recovery.Qualification = &q
	if _, err = tasktoolrelease.Recovery(stopped, in, true, now.Add(time.Second)); err == nil {
		t.Fatal("qualification before executor exit")
	}
	request.Execution.Action = "executor_exited"
	settled, err := tasktoolrelease.Execution(stopped, request, false, now)
	if err != nil || settled.Execution.Recovery.State != "settled" || stopped.Execution.ExecutorExitedAt != nil {
		t.Fatal("exit acknowledgement failed", err)
	}
	q = qualification(settled, now.Add(time.Second))
	in.Recovery.Qualification = &q
	qualified, err := tasktoolrelease.Recovery(settled, in, true, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if qualified.Execution.Recovery.Decision != "" || qualified.ReadyToApply || qualified.BindingsVerified {
		t.Fatal("qualification granted authority")
	}
	in.Decision = "recovery_approve"
	in.Recovery.Qualification = nil
	in.Recovery.QualificationSHA256 = tasktoolrelease.QualificationDigest(q)
	approved, err := tasktoolrelease.Recovery(qualified, in, true, now.Add(2*time.Second))
	if err != nil || approved.Execution.Recovery.Decision != "approved" {
		t.Fatal("separate approval failed", err)
	}
	in.Decision = "recovery_revoke"
	revoked, err := tasktoolrelease.Recovery(approved, in, false, now.Add(2*time.Minute))
	if err != nil || revoked.Execution.Recovery.Decision != "revoked" {
		t.Fatal("revoke after expiry/drift", err)
	}
	in.Decision = "recovery_approve"
	if _, err = tasktoolrelease.Recovery(revoked, in, true, now.Add(3*time.Second)); err == nil {
		t.Fatal("revoked approval revived")
	}
	if _, err = tasktoolrelease.Recovery(qualified, in, true, q.ExpiresAt); err == nil {
		t.Fatal("expired qualification approved")
	}
	if _, err = tasktoolrelease.Recovery(qualified, in, false, now.Add(3*time.Second)); err == nil {
		t.Fatal("changed native generation approved")
	}
}
func TestMCPTaskRecoveryRejectsRebindingAndLegacyExecutors(t *testing.T) {
	for _, change := range []string{"legacy", "operation", "revision", "intent", "owner"} {
		t.Run(change, func(t *testing.T) {
			r, in, now := handoverFixture(t)
			switch change {
			case "legacy":
				r.Execution.FenceVersion = 0
			case "operation":
				in.Recovery.OperationID = strings.Repeat("4", 64)
			case "revision":
				in.Recovery.Revision++
			case "intent":
				in.IntentSHA256 = strings.Repeat("5", 64)
			case "owner":
				in.AuthorizerKeyID = ""
			}
			if _, err := tasktoolrelease.Recovery(r, in, true, now); err == nil {
				t.Fatal("rebound handover accepted")
			}
		})
	}
	r, in, now := handoverFixture(t)
	exited, err := tasktoolrelease.Execution(r, exitRequest(r), false, now)
	if err != nil {
		t.Fatal(err)
	}
	settled, err := tasktoolrelease.Recovery(exited, in, true, now)
	if err != nil || settled.Execution.Recovery.State != "settled" {
		t.Fatal("already-exited executor not settled", err)
	}
	for _, change := range []string{"action", "target", "serving", "bindings", "revision", "deadline", "future", "configuration", "app", "generation", "duplicate", "pending"} {
		t.Run(change, func(t *testing.T) {
			q := qualification(settled, now)
			switch change {
			case "action":
				q.Action = "resume"
			case "target":
				q.TargetDeploymentID = uuid.NewString()
			case "serving":
				q.ExpectedServingDeploymentID = uuid.NewString()
			case "bindings":
				q.BindingsSHA256 = strings.Repeat("6", 64)
			case "revision":
				q.Revision++
			case "deadline":
				q.ExpiresAt = q.ExpiresAt.Add(time.Second)
			case "future":
				q.ObservedAt = q.ObservedAt.Add(time.Second)
			case "configuration":
				q.NativeApps[0].Review.ConfigurationHash = strings.Repeat("7", 64)
			case "app":
				q.NativeApps[0].Review.AppID = uuid.NewString()
			case "generation":
				q.NativeApps[0].Review.LatestDeploymentID = uuid.NewString()
			case "duplicate":
				q.NativeApps[1] = q.NativeApps[0]
			case "pending":
				q.TargetDeploymentID = ""
			}
			in.Decision = "recovery_qualify"
			in.Recovery.Qualification = &q
			if _, err := tasktoolrelease.Recovery(settled, in, true, now); err == nil {
				t.Fatal("changed qualification accepted")
			}
		})
	}
}
