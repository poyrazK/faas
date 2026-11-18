package tasktoolrelease

import (
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

// ValidRecoveryMetadata validates historical identities without granting live
// authority or rejecting a consumed approval merely because it has expired.
func ValidRecoveryMetadata(r api.TaskToolReleaseIntentRecord) bool {
	e := r.Execution
	if !validRecoveryHandoverMetadata(e) {
		return false
	}
	if e == nil || e.Recovery == nil {
		return true
	}
	h := e.Recovery
	if e.State != "reconcile_required" {
		return false
	}
	if h.Qualification != nil && !validQualification(r, *h.Qualification, h.Qualification.ObservedAt) {
		return false
	}
	if h.Result != nil && (h.Decision == "" || !ValidRecoveryResult(r)) {
		return false
	}
	return ValidQuarantine(r) && ValidRetirement(r)
}

// Metadata validation never treats an old qualification or verdict as live authority.
func validRecoveryHandoverMetadata(e *api.TaskToolReleaseExecution) bool {
	if e == nil {
		return true
	}
	if e.FenceVersion != 0 && e.FenceVersion != 1 {
		return false
	}
	if e.ExecutorExitedAt != nil && (e.FenceVersion != 1 || e.ExecutorExitedAt.Before(e.AuthorizedAt) || e.State == "authorized" || e.State == "running") {
		return false
	}
	h := e.Recovery
	if h == nil {
		return true
	}
	if e.FenceVersion != 1 || e.Receipt == nil || !toolpublication.ValidID(h.ID) || h.OperationID != e.OperationID || h.Revision != e.Receipt.Revision || h.RequestedAt.Before(e.AuthorizedAt) {
		return false
	}
	if h.State == "stopping" {
		return h.SettledAt == nil && e.ExecutorExitedAt == nil && h.Qualification == nil && h.Decision == "" && h.Result == nil && h.Quarantine == nil && h.Retirement == nil
	}
	if h.State != "settled" || h.SettledAt == nil || e.ExecutorExitedAt == nil || h.SettledAt.Before(h.RequestedAt) || h.SettledAt.Before(*e.ExecutorExitedAt) {
		return false
	}
	if h.Decision != "" && (h.Decision != "approved" && h.Decision != "revoked" || uuid.Validate(h.AuthorizerKeyID) != nil || h.DecidedAt == nil) {
		return false
	}
	if h.Qualification == nil {
		return h.Decision == "" && h.Result == nil && h.Quarantine == nil && h.Retirement == nil
	}
	q := h.Qualification
	return q.HandoverID == h.ID && q.OperationID == e.OperationID && q.Revision == h.Revision && q.Action == "restore" &&
		q.BindingsSHA256 == e.BindingsSHA256 && toolpublication.ValidID(q.EvidenceSHA256) && q.TargetDeploymentID != "" && q.TargetDeploymentID == e.Receipt.ServingDeploymentID &&
		q.ExpectedServingDeploymentID != "" && q.ExpectedServingDeploymentID == e.Receipt.WebDeploymentID &&
		!q.ObservedAt.Before(*h.SettledAt) && q.ExpiresAt.Equal(q.ObservedAt.Add(api.TaskToolRecoveryApprovalTTL))
}
