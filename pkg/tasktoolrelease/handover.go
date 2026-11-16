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

// QualificationDigest binds the action, checkpoint, target, evidence and deadline.
func QualificationDigest(q api.TaskToolRecoveryQualification) string {
	b, _ := json.Marshal(q)
	return toolpublication.Digest(b)
}

// Recovery changes owner recovery authority, never deployment or Task state.
// fresh is a locked native snapshot and current owner credential check.
func Recovery(record api.TaskToolReleaseIntentRecord, in api.TaskToolReleaseIntentDecision, fresh bool, now time.Time) (api.TaskToolReleaseIntentRecord, error) {
	r := in.Recovery
	e := record.Execution
	if r == nil || e == nil || !toolpublication.ValidID(r.HandoverID) || r.OperationID != e.OperationID ||
		e.Receipt == nil || r.Revision != e.Receipt.Revision || in.IntentSHA256 != record.IntentSHA256 ||
		in.ReleaseFingerprint != record.Intent.ReleaseFingerprint || uuid.Validate(in.AuthorizerKeyID) != nil {
		return record, ErrChanged
	}
	body, _ := json.Marshal(e)
	var copy api.TaskToolReleaseExecution
	_ = json.Unmarshal(body, &copy)
	record.Execution, e = &copy, &copy
	if in.Decision == "recovery_stop" {
		if e.Recovery != nil {
			if e.Recovery.ID != r.HandoverID || e.Recovery.Revision != r.Revision {
				return record, ErrChanged
			}
			return record, nil // lost reply: no replacement handover or deadline
		}
		if e.FenceVersion != 1 || e.State != "running" && e.State != "reconcile_required" {
			return record, ErrChanged
		}
		h := &api.TaskToolRecoveryHandover{ID: r.HandoverID, OperationID: e.OperationID, Revision: r.Revision, State: "stopping", RequestedAt: now.UTC()}
		if e.ExecutorExitedAt != nil {
			stamp := now.UTC()
			h.SettledAt = &stamp
			h.State = "settled"
		}
		e.Recovery, e.State = h, "reconcile_required"
		return record, nil
	}
	h := e.Recovery
	if h == nil || h.ID != r.HandoverID || h.OperationID != r.OperationID || h.Revision != r.Revision {
		return record, ErrChanged
	}
	if in.Decision == "recovery_revoke" {
		if h.Qualification == nil || r.QualificationSHA256 != QualificationDigest(*h.Qualification) {
			return record, ErrChanged
		}
		if h.Decision != "revoked" {
			stamp := now.UTC()
			h.Decision, h.AuthorizerKeyID, h.DecidedAt = "revoked", in.AuthorizerKeyID, &stamp
		}
		return record, nil
	}
	if h.State != "settled" || h.SettledAt == nil || e.ExecutorExitedAt == nil {
		return record, ErrChanged
	}
	if in.Decision == "recovery_qualify" {
		q := r.Qualification
		if q == nil || !validQualification(record, *q, now) {
			return record, ErrChanged
		}
		if h.Qualification != nil {
			if !reflect.DeepEqual(h.Qualification, q) {
				return record, ErrChanged
			}
			return record, nil
		}
		if !fresh {
			return record, ErrChanged
		}
		h.Qualification = q
		return record, nil
	}
	if in.Decision != "recovery_approve" || h.Qualification == nil ||
		r.QualificationSHA256 != QualificationDigest(*h.Qualification) || !validQualification(record, *h.Qualification, now) || !fresh {
		return record, ErrChanged
	}
	if h.Decision != "" {
		if h.Decision != "approved" || h.AuthorizerKeyID != in.AuthorizerKeyID {
			return record, ErrChanged
		}
		return record, nil
	}
	stamp := now.UTC()
	h.Decision, h.AuthorizerKeyID, h.DecidedAt = "approved", in.AuthorizerKeyID, &stamp
	return record, nil
}

func validQualification(record api.TaskToolReleaseIntentRecord, q api.TaskToolRecoveryQualification, now time.Time) bool {
	e := record.Execution
	h, receipt := e.Recovery, e.Receipt
	if h == nil || q.HandoverID != h.ID || q.OperationID != e.OperationID || q.Revision != receipt.Revision ||
		q.Action != "restore" || q.TargetDeploymentID == "" || q.TargetDeploymentID != receipt.ServingDeploymentID ||
		q.ExpectedServingDeploymentID == "" || q.ExpectedServingDeploymentID != receipt.WebDeploymentID ||
		q.TargetDeploymentID == q.ExpectedServingDeploymentID || receipt.PendingSubmission != "" ||
		q.BindingsSHA256 != e.BindingsSHA256 || !toolpublication.ValidID(q.EvidenceSHA256) ||
		q.ObservedAt.Before(*h.SettledAt) || q.ObservedAt.After(now) || !now.Before(q.ExpiresAt) ||
		!q.ExpiresAt.Equal(q.ObservedAt.Add(api.TaskToolRecoveryApprovalTTL)) || len(q.NativeApps) != len(record.Intent.Apps) {
		return false
	}
	seen := map[string]bool{}
	for _, pin := range record.Intent.Apps {
		found := false
		for _, current := range q.NativeApps {
			if current.App != pin.App || current.Role != pin.Role {
				continue
			}
			if seen[current.App] || current.Review.AppID != pin.Review.AppID || current.Review.Scope != api.DefaultEnvScope ||
				current.Review.ConfigurationHash != pin.Review.ConfigurationHash {
				return false
			}
			if current.Role == "web" && (current.Review.LatestDeploymentID != receipt.WebDeploymentID || !reflect.DeepEqual(current.Review.ServingDeploymentIDs, []string{receipt.WebDeploymentID})) {
				return false
			}
			if current.Role == "previous_worker" && current.Review.LatestDeploymentID != receipt.PreviousDeployments[current.App] {
				return false
			}
			if current.Role == "worker" && current.Review.LatestDeploymentID != receipt.WorkerDeploymentID {
				return false
			}
			seen[current.App], found = true, true
		}
		if !found {
			return false
		}
	}
	return true
}

// IsRecoveryDecision identifies the closed owner-only transition namespace.
func IsRecoveryDecision(decision string) bool { return strings.HasPrefix(decision, "recovery_") }
