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

func IsRetirementChange(action string, in api.TaskToolReleaseIntentDecision) bool {
	return action == "retirement" || strings.HasPrefix(in.Decision, "recovery_retire_")
}
func RetirementIdentity(r api.TaskToolReleaseIntentRecord, digest string) bool {
	return r.Execution != nil && r.Execution.Recovery != nil && r.Execution.Recovery.Retirement != nil && toolpublication.ValidID(digest) && QualificationDigest(r.Execution.Recovery.Retirement.Qualification) == digest
}
func retirementBase(r api.TaskToolReleaseIntentRecord) bool {
	return ValidRecoveryResult(r) && ValidQuarantine(r) && r.Execution.Recovery.Quarantine != nil && r.Execution.Recovery.Quarantine.Result != nil && r.Execution.FenceVersion == 1 && r.Execution.Recovery.State == "settled" && r.Execution.ExecutorExitedAt != nil
}
func validRetirementQualification(r api.TaskToolReleaseIntentRecord, q api.TaskToolRecoveryQualification) bool {
	return retirementBase(r) && validCleanupQualification(r, q, "retire", r.Execution.Recovery.Quarantine.Result.ObservedAt)
}

// Retirement only prepares metadata. A park change must be persisted by the
// concrete native parking transaction together with its actual transition ID.
func Retirement(r api.TaskToolReleaseIntentRecord, in api.TaskToolReleaseIntentDecision, fresh bool, now time.Time, transition string) (api.TaskToolReleaseIntentRecord, error) {
	if !retirementBase(r) || in.Retirement == nil || in.ReleaseFingerprint != r.Intent.ReleaseFingerprint {
		return r, ErrChanged
	}
	b, _ := json.Marshal(r)
	var out api.TaskToolReleaseIntentRecord
	_ = json.Unmarshal(b, &out)
	r = out
	h, req := r.Execution.Recovery, in.Retirement
	q := h.Retirement
	if strings.HasPrefix(in.Decision, "recovery_retire_") {
		if req.Action != "" || in.IntentSHA256 != r.IntentSHA256 || uuid.Validate(in.AuthorizerKeyID) != nil {
			return r, ErrChanged
		}
		if in.Decision == "recovery_retire_qualify" {
			if req.Qualification == nil || !validRetirementQualification(r, *req.Qualification) || !fresh || !quarantineDeadline(*req.Qualification, now) {
				return r, ErrChanged
			}
			if q != nil {
				if !reflect.DeepEqual(q.Qualification, *req.Qualification) {
					return r, ErrChanged
				}
				return r, nil
			}
			h.Retirement = &api.TaskToolRetirement{Qualification: *req.Qualification, State: "qualified"}
			return r, nil
		}
		if req.Qualification != nil || q == nil || !RetirementIdentity(r, req.QualificationSHA256) {
			return r, ErrChanged
		}
		if in.Decision == "recovery_retire_revoke" {
			stamp := now.UTC()
			q.Decision, q.AuthorizerKeyID, q.DecidedAt = "revoked", in.AuthorizerKeyID, &stamp
			return r, nil
		}
		if in.Decision != "recovery_retire_approve" || !fresh || !quarantineDeadline(q.Qualification, now) || q.State != "qualified" || q.Decision == "revoked" || q.Decision == "approved" && q.AuthorizerKeyID != in.AuthorizerKeyID {
			return r, ErrChanged
		}
		if q.Decision == "" {
			stamp := now.UTC()
			q.Decision, q.AuthorizerKeyID, q.DecidedAt = "approved", in.AuthorizerKeyID, &stamp
		}
		return r, nil
	}
	if req.Qualification != nil || !RetirementIdentity(r, req.QualificationSHA256) || !ValidRetirement(r) || req.BindingsSHA256 != r.Execution.BindingsSHA256 || !toolpublication.ValidID(req.ClaimToken) || toolpublication.Digest([]byte(req.ClaimToken)) != r.Execution.ClaimSHA256 {
		return r, ErrChanged
	}
	if req.Action == "park" {
		if q.State != "qualified" || q.Decision != "approved" || !fresh || !quarantineDeadline(q.Qualification, now) || uuid.Validate(transition) != nil || req.ShutdownTimeoutMS <= 0 || !validQuarantineInstances(q.Qualification, req.ExpectedInstanceIDs) || req.ExpectedMCPAccessPolicy == nil || req.ExpectedMCPAccessPolicy.Validate() != nil || !freshRetirementEvidence(req, q.Qualification.ObservedAt, now) {
			return r, ErrChanged
		}
		stamp := now.UTC()
		q.State, q.ParkTransitionID, q.ParkRequestedAt = "retirement_pending", transition, &stamp
		return r, nil
	}
	if req.Action != "observe" || q.ParkRequestedAt == nil {
		return r, ErrChanged
	}
	if q.Result != nil {
		return r, nil
	}
	if q.State != "retirement_pending" || !fresh || !freshRetirementEvidence(req, *q.ParkRequestedAt, now) {
		return r, ErrChanged
	}
	q.Result = &api.TaskToolRetirementResult{OperationID: r.Execution.OperationID, HandoverID: h.ID, QualificationSHA256: req.QualificationSHA256, WorkerDeploymentID: r.Execution.Receipt.WorkerDeploymentID, ServingDeploymentID: r.Execution.Receipt.ServingDeploymentID, WorkerIDs: append([]string{}, r.Execution.Receipt.WorkerIDs...), ParkTransitionID: q.ParkTransitionID, EvidenceSHA256: req.EvidenceSHA256, ObservedAt: req.CheckedAt.UTC(), RecordedAt: now.UTC()}
	q.State = "retired"
	return r, nil
}
func freshRetirementEvidence(r *api.TaskToolRetirementRequest, since, now time.Time) bool {
	return toolpublication.ValidID(r.EvidenceSHA256) && !r.CheckedAt.Before(since) && !r.CheckedAt.After(now) && now.Sub(r.CheckedAt) <= api.TaskToolRecoveryObservationTTL
}

// ValidRetirement checks historical identities; a retained result is not a
// current health/capacity claim and never authorizes another parking request.
func ValidRetirement(r api.TaskToolReleaseIntentRecord) bool {
	if r.Execution == nil || r.Execution.Recovery == nil || r.Execution.Recovery.Retirement == nil {
		return true
	}
	if !retirementBase(r) {
		return false
	}
	q := r.Execution.Recovery.Retirement
	if !validRetirementQualification(r, q.Qualification) || q.Decision != "" && (q.Decision != "approved" && q.Decision != "revoked" || uuid.Validate(q.AuthorizerKeyID) != nil || q.DecidedAt == nil || q.DecidedAt.Before(q.Qualification.ObservedAt)) {
		return false
	}
	if q.State == "qualified" {
		return q.ParkTransitionID == "" && q.ParkRequestedAt == nil && q.Result == nil
	}
	if q.Decision == "" || q.ParkRequestedAt == nil || q.Decision == "approved" && q.DecidedAt.After(*q.ParkRequestedAt) || q.ParkRequestedAt.Before(q.Qualification.ObservedAt) || !q.ParkRequestedAt.Before(q.Qualification.ExpiresAt) || uuid.Validate(q.ParkTransitionID) != nil {
		return false
	}
	if q.State == "retirement_pending" {
		return q.Result == nil
	}
	x := q.Result
	return q.State == "retired" && x != nil && x.OperationID == r.Execution.OperationID && x.HandoverID == r.Execution.Recovery.ID && x.QualificationSHA256 == QualificationDigest(q.Qualification) && x.WorkerDeploymentID == r.Execution.Receipt.WorkerDeploymentID && x.ServingDeploymentID == r.Execution.Receipt.ServingDeploymentID && reflect.DeepEqual(x.WorkerIDs, r.Execution.Receipt.WorkerIDs) && x.ParkTransitionID == q.ParkTransitionID && toolpublication.ValidID(x.EvidenceSHA256) && !x.ObservedAt.Before(*q.ParkRequestedAt) && !x.RecordedAt.Before(x.ObservedAt)
}
