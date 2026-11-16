package tasktoolrelease_test

import (
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
	"strings"
	"testing"
	"time"
)

func quarantineFixture(t *testing.T) (api.TaskToolReleaseIntentRecord, api.TaskToolReleaseIntentDecision, time.Time) {
	t.Helper()
	r, restore, now := restoreFixture(t)
	var err error
	r, err = tasktoolrelease.Restore(r, restore, now)
	if err != nil {
		t.Fatal(err)
	}
	q := *r.Execution.Recovery.Qualification
	q.Action = "quarantine"
	q.TargetDeploymentID = r.Execution.Receipt.WorkerDeploymentID
	q.ExpectedServingDeploymentID = r.Execution.Receipt.ServingDeploymentID
	q.NativeApps = append([]api.TaskToolReleaseApp{}, q.NativeApps...)
	for i := range q.NativeApps {
		if q.NativeApps[i].Role == "web" {
			q.NativeApps[i].Review.ServingDeploymentIDs = []string{r.Execution.Receipt.ServingDeploymentID}
		}
	}
	in := api.TaskToolReleaseIntentDecision{IntentSHA256: r.IntentSHA256, ReleaseFingerprint: r.Intent.ReleaseFingerprint, AuthorizerKeyID: uuid.NewString(), Decision: "recovery_quarantine_qualify", Quarantine: &api.TaskToolQuarantineRequest{Qualification: &q}}
	r, err = tasktoolrelease.Quarantine(r, in, true, now)
	if err != nil {
		t.Fatal(err)
	}
	in.Decision = "recovery_quarantine_approve"
	in.Quarantine = &api.TaskToolQuarantineRequest{QualificationSHA256: tasktoolrelease.QualificationDigest(q)}
	r, err = tasktoolrelease.Quarantine(r, in, true, now)
	if err != nil {
		t.Fatal(err)
	}
	in.Decision = ""
	in.Quarantine = &api.TaskToolQuarantineRequest{Action: "claim", QualificationSHA256: in.Quarantine.QualificationSHA256, ClaimToken: restore.ClaimToken, AttemptToken: strings.Repeat("6", 64), BindingsSHA256: r.Execution.BindingsSHA256, EvidenceSHA256: strings.Repeat("7", 64), CheckedAt: now, ExpectedInstanceIDs: restore.ExpectedInstanceIDs, ExpectedMCPAccessPolicy: restore.ExpectedMCPAccessPolicy}
	return r, in, now
}
func TestMCPTaskQuarantineSingleAdmissionAndHistoricalReconciliation(t *testing.T) {
	r, in, now := quarantineFixture(t)
	claimed, err := tasktoolrelease.Quarantine(r, in, true, now)
	if err != nil || claimed.Execution.Recovery.Quarantine.State != "applying" || r.Execution.Recovery.Quarantine.ClaimedAt != nil {
		t.Fatal("claim or immutability", err)
	}
	if _, err = tasktoolrelease.Quarantine(claimed, in, true, now); err == nil {
		t.Fatal("duplicate claim admitted another SQL attempt")
	}
	revoke := in
	revoke.Decision = "recovery_quarantine_revoke"
	revoke.Quarantine = &api.TaskToolQuarantineRequest{QualificationSHA256: in.Quarantine.QualificationSHA256}
	revoked, err := tasktoolrelease.Quarantine(claimed, revoke, false, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	in.Quarantine.Action = "observe"
	in.Quarantine.CheckedAt = now.Add(time.Hour)
	out, err := tasktoolrelease.Quarantine(revoked, in, false, now.Add(time.Hour))
	if err != nil || !tasktoolrelease.ValidQuarantine(out) || out.Execution.Recovery.Quarantine.Result == nil || out.ReadyToApply || out.BindingsVerified {
		t.Fatal("read-only reconciliation failed", err)
	}
	retry, err := tasktoolrelease.Quarantine(out, in, false, now.Add(2*time.Hour))
	if err != nil || !retry.Execution.Recovery.Quarantine.Result.RecordedAt.Equal(out.Execution.Recovery.Quarantine.Result.RecordedAt) {
		t.Fatal("result replay changed history", err)
	}
}
func TestMCPTaskQuarantineRejectsChangedAuthority(t *testing.T) {
	for _, change := range []string{"restore_only", "expired", "revoked", "freshness", "token", "attempt", "binding", "digest", "policy", "instances", "future", "early", "native", "worker", "traffic", "snapshot_override"} {
		t.Run(change, func(t *testing.T) {
			r, in, now := quarantineFixture(t)
			fresh := true
			switch change {
			case "restore_only":
				r.Execution.Recovery.Quarantine.Decision = ""
			case "expired":
				now = now.Add(api.TaskToolRecoveryApprovalTTL)
			case "revoked":
				r.Execution.Recovery.Quarantine.Decision = "revoked"
			case "freshness":
				in.Quarantine.CheckedAt = now.Add(-time.Second)
			case "token":
				in.Quarantine.ClaimToken = ""
			case "attempt":
				in.Quarantine.AttemptToken = ""
			case "binding":
				in.Quarantine.BindingsSHA256 = ""
			case "digest":
				in.Quarantine.QualificationSHA256 = strings.Repeat("9", 64)
			case "policy":
				in.Quarantine.ExpectedMCPAccessPolicy = nil
			case "instances":
				in.Quarantine.ExpectedInstanceIDs = nil
			case "future":
				in.Quarantine.CheckedAt = now.Add(time.Second)
			case "early":
				r.Execution.Recovery.Result = nil
			case "native":
				fresh = false
			case "worker":
				r.Execution.Recovery.Quarantine.Qualification.TargetDeploymentID = uuid.NewString()
			case "snapshot_override":
				q := r.Execution.Recovery.Quarantine.Qualification
				in.Quarantine.Qualification = &q
			case "traffic":
				r.Execution.Recovery.Quarantine.Qualification.ExpectedServingDeploymentID = r.Execution.Receipt.WebDeploymentID
			}
			if _, err := tasktoolrelease.Quarantine(r, in, fresh, now); err == nil {
				t.Fatal("changed authority admitted")
			}
		})
	}
}
