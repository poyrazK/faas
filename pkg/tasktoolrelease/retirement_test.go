package tasktoolrelease_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
)

func retirementFixture(t *testing.T) (api.TaskToolReleaseIntentRecord, api.TaskToolReleaseIntentDecision, time.Time) {
	t.Helper()
	r, in, now := quarantineFixture(t)
	var err error
	r, err = tasktoolrelease.Quarantine(r, in, true, now)
	if err != nil {
		t.Fatal(err)
	}
	in.Quarantine.Action = "observe"
	r, err = tasktoolrelease.Quarantine(r, in, true, now)
	if err != nil {
		t.Fatal(err)
	}
	q := r.Execution.Recovery.Quarantine.Qualification
	q.Action = "retire"
	owner := api.TaskToolReleaseIntentDecision{IntentSHA256: r.IntentSHA256, ReleaseFingerprint: r.Intent.ReleaseFingerprint, AuthorizerKeyID: uuid.NewString(), Decision: "recovery_retire_qualify", Retirement: &api.TaskToolRetirementRequest{Qualification: &q}}
	r, err = tasktoolrelease.Retirement(r, owner, true, now, "")
	if err != nil {
		t.Fatal(err)
	}
	owner.Decision = "recovery_retire_approve"
	owner.Retirement = &api.TaskToolRetirementRequest{QualificationSHA256: tasktoolrelease.QualificationDigest(q)}
	r, err = tasktoolrelease.Retirement(r, owner, true, now, "")
	if err != nil {
		t.Fatal(err)
	}
	return r, api.TaskToolReleaseIntentDecision{ReleaseFingerprint: r.Intent.ReleaseFingerprint, Retirement: &api.TaskToolRetirementRequest{Action: "park", QualificationSHA256: owner.Retirement.QualificationSHA256, ClaimToken: in.Quarantine.ClaimToken, BindingsSHA256: r.Execution.BindingsSHA256, EvidenceSHA256: strings.Repeat("8", 64), CheckedAt: now, ExpectedInstanceIDs: in.Quarantine.ExpectedInstanceIDs, ExpectedMCPAccessPolicy: in.Quarantine.ExpectedMCPAccessPolicy, ShutdownTimeoutMS: 30000}}, now
}
func TestMCPTaskRetirementSingleParkAndHistoricalDrain(t *testing.T) {
	r, in, now := retirementFixture(t)
	transition := uuid.NewString()
	pending, err := tasktoolrelease.Retirement(r, in, true, now, transition)
	if err != nil || !tasktoolrelease.ValidRetirement(pending) || pending.Execution.Recovery.Retirement.State != "retirement_pending" || pending.Execution.Recovery.Retirement.ParkTransitionID != transition || r.Execution.Recovery.Retirement.State != "qualified" {
		t.Fatal("atomic park metadata", err)
	}
	if _, err = tasktoolrelease.Retirement(pending, in, true, now, uuid.NewString()); err == nil {
		t.Fatal("duplicate parking admitted")
	}
	revoke := in
	revoke.IntentSHA256 = r.IntentSHA256
	revoke.AuthorizerKeyID = r.Execution.Recovery.Retirement.AuthorizerKeyID
	revoke.Decision = "recovery_retire_revoke"
	revoke.Retirement = &api.TaskToolRetirementRequest{QualificationSHA256: in.Retirement.QualificationSHA256}
	pending, err = tasktoolrelease.Retirement(pending, revoke, false, now.Add(time.Hour), "")
	if err != nil {
		t.Fatal(err)
	}
	in.Retirement.Action = "observe"
	in.Retirement.CheckedAt = now.Add(time.Hour)
	if _, err = tasktoolrelease.Retirement(pending, in, false, now.Add(time.Hour), ""); err == nil {
		t.Fatal("undrained native instances accepted")
	}
	out, err := tasktoolrelease.Retirement(pending, in, true, now.Add(time.Hour), "")
	if err != nil || !tasktoolrelease.ValidRetirement(out) || out.Execution.Recovery.Retirement.Result == nil || out.ReadyToApply || out.BindingsVerified {
		t.Fatal("historical drain reconciliation", err)
	}
	replay, err := tasktoolrelease.Retirement(out, in, false, now.Add(2*time.Hour), "")
	if err != nil || !replay.Execution.Recovery.Retirement.Result.RecordedAt.Equal(out.Execution.Recovery.Retirement.Result.RecordedAt) {
		t.Fatal("replay replaced history", err)
	}
	out.Execution.Recovery.Retirement.Result.ParkTransitionID = uuid.NewString()
	if tasktoolrelease.ValidRetirement(out) {
		t.Fatal("forged transition accepted")
	}
}
func TestMCPTaskRetirementRejectsChangedAuthority(t *testing.T) {
	for _, change := range []string{"restore_only", "quarantine_only", "expired", "revoked", "freshness", "claim", "binding", "digest", "policy", "instances", "future", "native", "worker", "traffic", "snapshot_override", "grace", "transition"} {
		t.Run(change, func(t *testing.T) {
			r, in, now := retirementFixture(t)
			fresh := true
			transition := uuid.NewString()
			switch change {
			case "restore_only":
				r.Execution.Recovery.Quarantine = nil
			case "quarantine_only":
				r.Execution.Recovery.Retirement.Decision = ""
			case "expired":
				now = now.Add(api.TaskToolRecoveryApprovalTTL)
			case "revoked":
				r.Execution.Recovery.Retirement.Decision = "revoked"
			case "freshness":
				in.Retirement.CheckedAt = now.Add(-time.Second)
			case "claim":
				in.Retirement.ClaimToken = ""
			case "binding":
				in.Retirement.BindingsSHA256 = ""
			case "digest":
				in.Retirement.QualificationSHA256 = strings.Repeat("9", 64)
			case "policy":
				in.Retirement.ExpectedMCPAccessPolicy = nil
			case "instances":
				in.Retirement.ExpectedInstanceIDs = nil
			case "future":
				in.Retirement.CheckedAt = now.Add(time.Second)
			case "native":
				fresh = false
			case "worker":
				r.Execution.Recovery.Retirement.Qualification.TargetDeploymentID = uuid.NewString()
			case "traffic":
				r.Execution.Recovery.Retirement.Qualification.ExpectedServingDeploymentID = r.Execution.Receipt.WebDeploymentID
			case "snapshot_override":
				q := r.Execution.Recovery.Retirement.Qualification
				in.Retirement.Qualification = &q
			case "grace":
				in.Retirement.ShutdownTimeoutMS = 0
			case "transition":
				transition = ""
			}
			if _, err := tasktoolrelease.Retirement(r, in, fresh, now, transition); err == nil {
				t.Fatal("changed retirement authority admitted")
			}
		})
	}
}
