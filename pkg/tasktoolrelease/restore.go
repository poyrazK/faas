package tasktoolrelease

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

var ErrRecoveryApplied = errors.New("task recovery was already committed")

// RestoreIdentity binds both first attempts and retained-result reads. Replay
// never certifies the current traffic or renews the approval deadline.
func RestoreIdentity(record api.TaskToolReleaseIntentRecord, in api.TaskToolRecoveryRestoreRequest) bool {
	e := record.Execution
	return e != nil && e.Recovery != nil && e.Recovery.Qualification != nil && toolpublication.ValidID(in.OperationID) && toolpublication.ValidID(in.HandoverID) && toolpublication.ValidID(in.QualificationSHA256) &&
		e.OperationID == in.OperationID && e.Recovery.ID == in.HandoverID && QualificationDigest(*e.Recovery.Qualification) == in.QualificationSHA256
}

// Restore prepares the receipt for the traffic transaction. The store must
// commit it with the exact guarded traffic write, never as a separate claim.
func Restore(record api.TaskToolReleaseIntentRecord, in api.TaskToolRecoveryRestoreRequest, now time.Time) (api.TaskToolReleaseIntentRecord, error) {
	if !RestoreIdentity(record, in) {
		return record, ErrChanged
	}
	e, h := record.Execution, record.Execution.Recovery
	if h.Result != nil {
		if !ValidRecoveryResult(record) {
			return record, ErrChanged
		}
		return record, ErrRecoveryApplied
	}
	if e.FenceVersion != 1 || e.State != "reconcile_required" || e.ExecutorExitedAt == nil || h.State != "settled" || h.SettledAt == nil || h.Decision != "approved" ||
		!validQualification(record, *h.Qualification, now) || !validRecoveryInstanceSnapshot(record, in.ExpectedInstanceIDs) || !toolpublication.ValidID(in.ClaimToken) || toolpublication.Digest([]byte(in.ClaimToken)) != e.ClaimSHA256 ||
		in.BindingsSHA256 != e.BindingsSHA256 || !toolpublication.ValidID(in.EvidenceSHA256) || in.CheckedAt.Before(h.Qualification.ObservedAt) || in.CheckedAt.After(now) || now.Sub(in.CheckedAt) > api.TaskToolRecoveryObservationTTL ||
		(in.ExpectedMCPAccessPolicy == nil || in.ExpectedMCPAccessPolicy.Validate() != nil) {
		return record, ErrChanged
	}
	body, _ := json.Marshal(record)
	var out api.TaskToolReleaseIntentRecord
	_ = json.Unmarshal(body, &out)
	out.Execution.Recovery.Result = &api.TaskToolRecoveryResult{IntentID: record.ID, OperationID: e.OperationID, HandoverID: h.ID, QualificationSHA256: in.QualificationSHA256, Revision: h.Revision, Action: "restore", TargetDeploymentID: h.Qualification.TargetDeploymentID,
		PreviousServingDeploymentID: h.Qualification.ExpectedServingDeploymentID, EvidenceSHA256: in.EvidenceSHA256, CheckedAt: in.CheckedAt.UTC(), CommittedAt: now.UTC()}
	return out, nil
}

// ValidRecoveryResult validates retained commit identity without treating the
// receipt as a fresh health observation or requiring an unexpired old approval.
func ValidRecoveryResult(record api.TaskToolReleaseIntentRecord) bool {
	e := record.Execution
	if e == nil || e.Recovery == nil || e.Recovery.Qualification == nil || e.Recovery.Result == nil {
		return false
	}
	h, q, result := e.Recovery, e.Recovery.Qualification, e.Recovery.Result
	return result.IntentID == record.ID && result.OperationID == e.OperationID && result.HandoverID == h.ID && result.QualificationSHA256 == QualificationDigest(*q) && result.Revision == h.Revision && result.Action == "restore" &&
		result.TargetDeploymentID == q.TargetDeploymentID && result.PreviousServingDeploymentID == q.ExpectedServingDeploymentID && toolpublication.ValidID(result.EvidenceSHA256) && !result.CheckedAt.Before(q.ObservedAt) && !result.CommittedAt.Before(result.CheckedAt) && result.CommittedAt.Before(q.ExpiresAt)
}

func validRecoveryInstanceSnapshot(record api.TaskToolReleaseIntentRecord, snapshot map[string][]string) bool {
	apps := record.Execution.Recovery.Qualification.NativeApps
	if len(snapshot) != len(apps) {
		return false
	}
	for _, app := range apps {
		ids := snapshot[app.Review.AppID]
		if len(ids) == 0 || len(ids) > api.TaskToolReleaseReceiptWorkerIDsMax {
			return false
		}
		seen := map[string]bool{}
		for _, id := range ids {
			if uuid.Validate(id) != nil || seen[id] {
				return false
			}
			seen[id] = true
		}
	}
	return true
}
