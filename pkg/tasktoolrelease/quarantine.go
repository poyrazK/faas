package tasktoolrelease

import (
	"encoding/json"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func IsQuarantineChange(action string, in api.TaskToolReleaseIntentDecision) bool {
	return action == "quarantine" || strings.HasPrefix(in.Decision, "recovery_quarantine_")
}

func QuarantineIdentity(r api.TaskToolReleaseIntentRecord, digest string) bool {
	return r.Execution != nil && r.Execution.Recovery != nil && r.Execution.Recovery.Quarantine != nil && toolpublication.ValidID(digest) && QualificationDigest(r.Execution.Recovery.Quarantine.Qualification) == digest
}

// Quarantine records one admission, not an atomic native/Task-database write.
// Repeated claims never authorize another SQL attempt. Observe only records
// read-only evidence and may reconcile an admitted attempt after revocation.
func Quarantine(r api.TaskToolReleaseIntentRecord, in api.TaskToolReleaseIntentDecision, fresh bool, now time.Time) (api.TaskToolReleaseIntentRecord, error) {
	if !ValidRecoveryResult(r) || r.Execution.FenceVersion != 1 || r.Execution.Recovery.State != "settled" || r.Execution.ExecutorExitedAt == nil || in.Quarantine == nil || in.ReleaseFingerprint != r.Intent.ReleaseFingerprint {
		return r, ErrChanged
	}
	b, _ := json.Marshal(r)
	var out api.TaskToolReleaseIntentRecord
	_ = json.Unmarshal(b, &out)
	r = out
	h, req := r.Execution.Recovery, in.Quarantine
	q := h.Quarantine
	if strings.HasPrefix(in.Decision, "recovery_quarantine_") {
		if req.Action != "" || in.IntentSHA256 != r.IntentSHA256 || uuid.Validate(in.AuthorizerKeyID) != nil {
			return r, ErrChanged
		}
		if in.Decision == "recovery_quarantine_qualify" {
			if req.Qualification == nil || !validQuarantineQualification(r, *req.Qualification) || !fresh || !quarantineDeadline(*req.Qualification, now) {
				return r, ErrChanged
			}
			if q != nil {
				if !reflect.DeepEqual(q.Qualification, *req.Qualification) {
					return r, ErrChanged
				}
				return r, nil
			}
			h.Quarantine = &api.TaskToolQuarantine{Qualification: *req.Qualification, State: "qualified"}
			return r, nil
		}
		if req.Qualification != nil || q == nil || !QuarantineIdentity(r, req.QualificationSHA256) {
			return r, ErrChanged
		}
		if in.Decision == "recovery_quarantine_revoke" {
			stamp := now.UTC()
			q.Decision, q.AuthorizerKeyID, q.DecidedAt = "revoked", in.AuthorizerKeyID, &stamp
			return r, nil
		}
		if in.Decision != "recovery_quarantine_approve" || !fresh || !quarantineDeadline(q.Qualification, now) || q.State != "qualified" || q.Decision == "revoked" || q.Decision == "approved" && q.AuthorizerKeyID != in.AuthorizerKeyID {
			return r, ErrChanged
		}
		if q.Decision == "" {
			stamp := now.UTC()
			q.Decision, q.AuthorizerKeyID, q.DecidedAt = "approved", in.AuthorizerKeyID, &stamp
		}
		return r, nil
	}
	if req.Qualification != nil || q == nil || !ValidQuarantine(r) || !QuarantineIdentity(r, req.QualificationSHA256) || !toolpublication.ValidID(req.AttemptToken) {
		return r, ErrChanged
	}
	if req.Action == "claim" {
		if q.State != "qualified" || q.Decision != "approved" || !fresh || !quarantineDeadline(q.Qualification, now) || req.BindingsSHA256 != r.Execution.BindingsSHA256 || !toolpublication.ValidID(req.ClaimToken) || toolpublication.Digest([]byte(req.ClaimToken)) != r.Execution.ClaimSHA256 || !freshQuarantineEvidence(req, q.Qualification.ObservedAt, now) || !validQuarantineInstances(q.Qualification, req.ExpectedInstanceIDs) || req.ExpectedMCPAccessPolicy == nil || req.ExpectedMCPAccessPolicy.Validate() != nil {
			return r, ErrChanged
		}
		stamp := now.UTC()
		q.State, q.ClaimedAt, q.AttemptSHA256 = "applying", &stamp, toolpublication.Digest([]byte(req.AttemptToken))
		return r, nil
	}
	if req.Action != "observe" || q.AttemptSHA256 != toolpublication.Digest([]byte(req.AttemptToken)) || q.ClaimedAt == nil {
		return r, ErrChanged
	}
	if q.Result != nil {
		return r, nil
	}
	if q.State != "applying" || !freshQuarantineEvidence(req, *q.ClaimedAt, now) {
		return r, ErrChanged
	}
	q.Result = &api.TaskToolQuarantineResult{OperationID: r.Execution.OperationID, HandoverID: h.ID, QualificationSHA256: req.QualificationSHA256, WorkerDeploymentID: r.Execution.Receipt.WorkerDeploymentID, ServingDeploymentID: r.Execution.Receipt.ServingDeploymentID, WorkerIDs: append([]string{}, r.Execution.Receipt.WorkerIDs...), EvidenceSHA256: req.EvidenceSHA256, ObservedAt: req.CheckedAt.UTC(), RecordedAt: now.UTC()}
	q.State = "quarantined"
	return r, nil
}

func freshQuarantineEvidence(req *api.TaskToolQuarantineRequest, since, now time.Time) bool {
	return toolpublication.ValidID(req.EvidenceSHA256) && !req.CheckedAt.Before(since) && !req.CheckedAt.After(now) && now.Sub(req.CheckedAt) <= api.TaskToolRecoveryObservationTTL
}
func quarantineDeadline(q api.TaskToolRecoveryQualification, now time.Time) bool {
	return !q.ObservedAt.After(now) && now.Before(q.ExpiresAt)
}
func validQuarantineInstances(q api.TaskToolRecoveryQualification, snapshot map[string][]string) bool {
	if len(snapshot) != len(q.NativeApps) {
		return false
	}
	for _, app := range q.NativeApps {
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

func validQuarantineQualification(r api.TaskToolReleaseIntentRecord, q api.TaskToolRecoveryQualification) bool {
	return validCleanupQualification(r, q, "quarantine", r.Execution.Recovery.Result.CommittedAt)
}
func validCleanupQualification(r api.TaskToolReleaseIntentRecord, q api.TaskToolRecoveryQualification, action string, since time.Time) bool {
	e, h, receipt := r.Execution, r.Execution.Recovery, r.Execution.Receipt
	if q.Action != action || q.OperationID != e.OperationID || q.HandoverID != h.ID || q.Revision != receipt.Revision || q.TargetDeploymentID != receipt.WorkerDeploymentID || q.TargetDeploymentID == "" || q.ExpectedServingDeploymentID != receipt.ServingDeploymentID || q.BindingsSHA256 != e.BindingsSHA256 || !toolpublication.ValidID(q.EvidenceSHA256) || q.ObservedAt.Before(since) || !q.ExpiresAt.Equal(q.ObservedAt.Add(api.TaskToolRecoveryApprovalTTL)) || len(q.NativeApps) != len(r.Intent.Apps) || len(receipt.WorkerIDs) == 0 {
		return false
	}
	seen := map[string]bool{}
	previous := 0
	for _, pin := range r.Intent.Apps {
		found := false
		for _, current := range q.NativeApps {
			if current.Role != pin.Role || current.App != pin.App {
				continue
			}
			if seen[current.App] || current.Review.AppID != pin.Review.AppID || current.Review.Scope != api.DefaultEnvScope || current.Review.ConfigurationHash != pin.Review.ConfigurationHash {
				return false
			}
			if current.Role == "web" && (current.Review.LatestDeploymentID != receipt.WebDeploymentID || !reflect.DeepEqual(current.Review.ServingDeploymentIDs, []string{receipt.ServingDeploymentID})) {
				return false
			}
			if current.Role == "worker" && current.Review.LatestDeploymentID != receipt.WorkerDeploymentID {
				return false
			}
			if current.Role == "previous_worker" && current.Review.LatestDeploymentID != receipt.PreviousDeployments[current.App] {
				return false
			}
			if current.Role == "previous_worker" {
				previous++
			}
			seen[current.App], found = true, true
		}
		if !found {
			return false
		}
	}
	return previous > 0
}

// ValidQuarantine checks historical metadata without renewing authority or
// certifying that previously observed workers remain fenced now.
func ValidQuarantine(r api.TaskToolReleaseIntentRecord) bool {
	if r.Execution == nil || r.Execution.Recovery == nil || r.Execution.Recovery.Quarantine == nil {
		return true
	}
	if !ValidRecoveryResult(r) {
		return false
	}
	q := r.Execution.Recovery.Quarantine
	if !validQuarantineQualification(r, q.Qualification) || q.Decision != "" && (q.Decision != "approved" && q.Decision != "revoked" || uuid.Validate(q.AuthorizerKeyID) != nil || q.DecidedAt == nil || q.DecidedAt.Before(q.Qualification.ObservedAt)) {
		return false
	}
	if q.State == "qualified" {
		return q.Result == nil && q.ClaimedAt == nil && q.AttemptSHA256 == ""
	}
	if q.Decision == "" || q.ClaimedAt == nil || q.Decision == "approved" && q.DecidedAt.After(*q.ClaimedAt) || q.ClaimedAt.Before(q.Qualification.ObservedAt) || !q.ClaimedAt.Before(q.Qualification.ExpiresAt) || !toolpublication.ValidID(q.AttemptSHA256) {
		return false
	}
	if q.State == "applying" {
		return q.Result == nil
	}
	x := q.Result
	return q.State == "quarantined" && x != nil && x.OperationID == r.Execution.OperationID && x.HandoverID == r.Execution.Recovery.ID && x.QualificationSHA256 == QualificationDigest(q.Qualification) && x.WorkerDeploymentID == r.Execution.Receipt.WorkerDeploymentID && x.ServingDeploymentID == r.Execution.Receipt.ServingDeploymentID && reflect.DeepEqual(x.WorkerIDs, r.Execution.Receipt.WorkerIDs) && toolpublication.ValidID(x.EvidenceSHA256) && !x.ObservedAt.Before(*q.ClaimedAt) && !x.RecordedAt.Before(x.ObservedAt)
}
