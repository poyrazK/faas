package state

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type readinessCoordinationUpdate struct {
	api.OperationWorkflowReadinessCoordinationRequest
	PreparedCompletion *api.OperationWorkflowReadinessCompletion
}

func prepareReadinessCompletion(ctx context.Context, store readinessSubscriptionBackend, account, tenant, app, scope, id string, sequence int64, req api.OperationWorkflowReadinessCoordinationRequest, operator bool) (readinessCoordinationUpdate, error) {
	out := readinessCoordinationUpdate{OperationWorkflowReadinessCoordinationRequest: req}
	if req.Completion == nil {
		return out, nil
	}
	r := req.Completion
	if req.FollowUpAt != nil || (r.Outcome != "resolved" && r.Outcome != "no_longer_applicable" && r.Outcome != "handed_off") || strings.TrimSpace(r.Note) == "" || len(r.Note) > api.OperationWorkflowReadinessFollowUpNoteMaxBytes || !utf8.ValidString(r.Note) || r.EvidenceOperationIDs == nil || len(r.EvidenceOperationIDs) > api.OperationWorkflowReadinessCompletionEvidenceMax || (r.Outcome == "resolved" && len(r.EvidenceOperationIDs) == 0) {
		return out, ErrInvalidArgument
	}
	ids := make([]string, 0, len(r.EvidenceOperationIDs))
	seen := map[string]bool{}
	for _, value := range r.EvidenceOperationIDs {
		parsed, err := uuid.Parse(value)
		if err != nil || seen[parsed.String()] {
			return out, ErrInvalidArgument
		}
		seen[parsed.String()] = true
		ids = append(ids, parsed.String())
	}
	check, err := checkReadinessNotification(ctx, store, account, tenant, app, scope, id, sequence, operator)
	if err != nil {
		return out, err
	}
	c := check.Notification.Coordination
	if c == nil || c.Revision != req.Revision || c.FollowUpAt == nil || c.Completion != nil {
		return out, ErrConflict
	}
	evidenceTenant := canonicalReadinessTenant(tenant)
	if operator && check.Notification.Change != nil {
		evidenceTenant = check.Notification.Change.Baseline.PlatformTenantID
	}
	for _, evidenceID := range ids {
		op, err := store.OperationByID(ctx, uuid.MustParse(account).String(), evidenceTenant, evidenceID)
		if err != nil {
			return out, err
		}
		if !sameOperationHistoryIdentity(op.AppID, app) || op.Scope != scope || (check.Notification.Change != nil && (!sameOperationHistoryIdentity(op.PlatformTenantID, evidenceTenant) || op.Subject == nil || *op.Subject != check.Notification.Change.Baseline.Subject)) {
			return out, ErrNotFound
		}
	}
	e := api.OperationWorkflowReadinessCompletionEvidence{CheckedAt: check.CheckedAt, Status: check.Status, Explanation: check.Handoff.Summary, RemainingReasons: []string{}, Warnings: append([]string{}, check.Handoff.Limitations...)}
	if check.Change != nil {
		e.ReportingOperationID = check.Change.CurrentOperationID
		if check.Handoff.Comparable && check.Change.Current != nil {
			current := check.Change.Current
			e.StateRevision = current.StateRevision
			e.EvidenceComplete = check.Change.EvidenceComparisonComplete
			e.RemainingReasons = append(e.RemainingReasons, current.Reasons...)
			e.PendingVerificationCount = current.PendingVerificationCount
			e.Warnings = append(e.Warnings, current.Advisories...)
		}
	}
	if check.Status != "ready" {
		e.Warnings = append(e.Warnings, "Follow-up completion does not establish workflow readiness: "+check.Status+".")
	}
	out.PreparedCompletion = &api.OperationWorkflowReadinessCompletion{Outcome: r.Outcome, Note: r.Note, EvidenceOperationIDs: ids, Evidence: e}
	return out, nil
}
