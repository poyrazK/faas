package tasktoolrelease_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
)

func restoreFixture(t *testing.T) (api.TaskToolReleaseIntentRecord, api.TaskToolRecoveryRestoreRequest, time.Time) {
	t.Helper()
	r, in, now := handoverFixture(t)
	var err error
	r, err = tasktoolrelease.Execution(r, exitRequest(r), true, now)
	if err != nil {
		t.Fatal(err)
	}
	r, err = tasktoolrelease.Recovery(r, in, true, now)
	if err != nil {
		t.Fatal(err)
	}
	q := qualification(r, now)
	in.Decision, in.Recovery.Qualification = "recovery_qualify", &q
	r, err = tasktoolrelease.Recovery(r, in, true, now)
	if err != nil {
		t.Fatal(err)
	}
	in.Decision, in.Recovery.Qualification, in.Recovery.QualificationSHA256 = "recovery_approve", nil, tasktoolrelease.QualificationDigest(q)
	r, err = tasktoolrelease.Recovery(r, in, true, now)
	if err != nil {
		t.Fatal(err)
	}
	request := api.TaskToolRecoveryRestoreRequest{OperationID: r.Execution.OperationID, HandoverID: r.Execution.Recovery.ID, QualificationSHA256: in.Recovery.QualificationSHA256, ClaimToken: strings.Repeat("1", 64), BindingsSHA256: r.Execution.BindingsSHA256, EvidenceSHA256: strings.Repeat("8", 64), CheckedAt: now}
	fingerprint, err := api.MCPAccessPolicyFingerprint("https://restore.example/mcp", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	request.ExpectedMCPAccessPolicy = &api.MCPAccessPolicyExpectation{Resource: "https://restore.example/mcp", Fingerprint: fingerprint}
	request.ExpectedInstanceIDs = map[string][]string{}
	for _, app := range r.Execution.Recovery.Qualification.NativeApps {
		request.ExpectedInstanceIDs[app.Review.AppID] = []string{uuid.NewString()}
	}
	return r, request, now
}
func TestMCPTaskRestoreConsumesExactApprovalOnce(t *testing.T) {
	r, in, now := restoreFixture(t)
	out, err := tasktoolrelease.Restore(r, in, now.Add(time.Second))
	if err != nil || !tasktoolrelease.ValidRecoveryResult(out) || r.Execution.Recovery.Result != nil {
		t.Fatal("atomic receipt preparation failed", err)
	}
	if out.ReadyToApply || out.BindingsVerified {
		t.Fatal("traffic commit certified runtime health")
	}
	// Replay remains an inspection after expiry or revocation; no new write authority.
	out.Execution.Recovery.Decision = "revoked"
	if _, err := tasktoolrelease.Restore(out, in, now.Add(time.Hour)); !errors.Is(err, tasktoolrelease.ErrRecoveryApplied) {
		t.Fatal("committed request acquired a second write", err)
	}
	out.Execution.Recovery.Result.TargetDeploymentID = out.Execution.Receipt.WebDeploymentID
	if tasktoolrelease.ValidRecoveryResult(out) {
		t.Fatal("foreign retained target accepted")
	}
}
func TestMCPTaskRestoreRejectsChangedAuthorityAndEvidence(t *testing.T) {
	for _, change := range []string{"operation", "handover", "digest", "token", "bindings", "evidence", "future", "old_check", "expired", "revoked", "unsettled", "legacy", "revision", "target", "old_approval", "missing_policy", "missing_instances"} {
		t.Run(change, func(t *testing.T) {
			r, in, now := restoreFixture(t)
			switch change {
			case "operation":
				in.OperationID = strings.Repeat("9", 64)
			case "handover":
				in.HandoverID = strings.Repeat("9", 64)
			case "digest":
				in.QualificationSHA256 = strings.Repeat("9", 64)
			case "token":
				in.ClaimToken = strings.Repeat("9", 64)
			case "bindings":
				in.BindingsSHA256 = strings.Repeat("9", 64)
			case "evidence":
				in.EvidenceSHA256 = ""
			case "future":
				in.CheckedAt = now.Add(time.Second)
			case "old_check":
				in.CheckedAt = now.Add(-time.Second)
			case "expired":
				now = now.Add(api.TaskToolRecoveryApprovalTTL)
			case "revoked":
				r.Execution.Recovery.Decision = "revoked"
			case "unsettled":
				r.Execution.Recovery.State = "stopping"
			case "legacy":
				r.Execution.FenceVersion = 0
			case "revision":
				r.Execution.Receipt.Revision++
			case "target":
				r.Execution.Recovery.Qualification.TargetDeploymentID = r.Execution.Receipt.WebDeploymentID
			case "old_approval":
				r.Execution.Recovery.Decision = ""
			case "missing_policy":
				in.ExpectedMCPAccessPolicy = nil
			case "missing_instances":
				in.ExpectedInstanceIDs = nil
			}
			if _, err := tasktoolrelease.Restore(r, in, now); err == nil {
				t.Fatal("changed restore admitted")
			}
		})
	}
	r, in, now := restoreFixture(t)
	r.Status = "revoked"
	if _, err := tasktoolrelease.Restore(r, in, now); err != nil {
		t.Fatal("separate recovery authority revived or depended on old release approval", err)
	}
}
