package state

import (
	"context"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func validateReadinessNotificationIdentity(account, tenant, app, scope, id string, sequence int64, operator bool) error {
	if id == "" || sequence < 1 || sequence > 9007199254740991 {
		return ErrInvalidArgument
	}
	return readinessSubscriptionIdentity(account, tenant, app, scope, id, operator)
}
func updateReadinessNotification(ctx context.Context, store readinessSubscriptionBackend, account, tenant, app, scope, id string, sequence int64, actor string, req api.OperationWorkflowReadinessCoordinationRequest, operator bool) (api.OperationWorkflowReadinessNotification, error) {
	var out api.OperationWorkflowReadinessNotification
	if err := validateReadinessNotificationIdentity(account, tenant, app, scope, id, sequence, operator); err != nil {
		return out, err
	}
	if actor == "" || req.Revision < 0 || req.Revision >= 9007199254740991 || (req.Status != "unread" && req.Status != "read" && req.Status != "acknowledged") || len(req.Owner) > api.OperationWorkflowReadinessFollowUpOwnerMaxBytes || len(req.Note) > api.OperationWorkflowReadinessFollowUpNoteMaxBytes || !utf8.ValidString(req.Owner) || !utf8.ValidString(req.Note) || strings.TrimSpace(req.Owner) != req.Owner || strings.IndexFunc(req.Owner, unicode.IsControl) >= 0 {
		return out, ErrInvalidArgument
	}
	if req.FollowUpAt != nil {
		t := req.FollowUpAt.UTC().Truncate(time.Microsecond)
		if t.Year() < 1 || t.Year() > 9999 {
			return out, ErrInvalidArgument
		}
		req.FollowUpAt = &t
	}
	prepared, err := prepareReadinessCompletion(ctx, store, account, tenant, app, scope, id, sequence, req, operator)
	if err != nil {
		return out, err
	}
	return store.updateReadinessNotification(ctx, account, tenant, app, scope, uuid.MustParse(id).String(), sequence, actor, prepared, operator)
}
func applyReadinessCoordination(n api.OperationWorkflowReadinessNotification, actor string, req readinessCoordinationUpdate) (api.OperationWorkflowReadinessNotification, error) {
	revision := int64(0)
	if n.Coordination != nil {
		revision = n.Coordination.Revision
	}
	if revision != req.Revision {
		return n, ErrConflict
	}
	if n.Coordination != nil && n.Coordination.Completion != nil && (req.Completion != nil || req.FollowUpAt != nil) {
		return n, ErrConflict
	}
	if req.Completion != nil && (req.PreparedCompletion == nil || n.Coordination == nil || n.Coordination.FollowUpAt == nil) {
		return n, ErrConflict
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	c := &api.OperationWorkflowReadinessCoordination{Revision: revision + 1, Status: req.Status, Owner: req.Owner, Note: req.Note, FollowUpAt: req.FollowUpAt, UpdatedAt: now, UpdatedBy: actor}
	if n.Coordination != nil {
		c.Completion = n.Coordination.Completion
	}
	if req.PreparedCompletion != nil {
		completion := *req.PreparedCompletion
		completion.CompletedAt, completion.CompletedBy = now, actor
		c.Completion = &completion
		c.FollowUpAt = nil
		if req.PreparedOutcomeWatch != nil {
			w := *req.PreparedOutcomeWatch
			if w.DeadlineAt != nil && !w.DeadlineAt.After(now) {
				return n, ErrConflict
			}
			w.StartedAt = now
			n.BusinessOutcomeWatch = &w
		}
		if req.PreparedWatch != nil {
			w := *req.PreparedWatch
			w.StartedAt = now
			n.VerificationWatch = &w
		}
	}
	if req.Status == "acknowledged" {
		c.AcknowledgedAt = &now
		c.AcknowledgedBy = actor
		if n.Coordination != nil && n.Coordination.Status == "acknowledged" {
			c.AcknowledgedAt = n.Coordination.AcknowledgedAt
			c.AcknowledgedBy = n.Coordination.AcknowledgedBy
		}
	}
	if n.Reminder != nil && (c.FollowUpAt == nil || !n.Reminder.DueAt.Equal(*c.FollowUpAt)) {
		n.Reminder = nil
	}
	n = readinessEscalationCoordination(n, c, actor, now)
	n.Coordination = c
	return n, nil
}
func checkReadinessNotification(ctx context.Context, store readinessSubscriptionBackend, account, tenant, app, scope, id string, sequence int64, operator bool) (out api.OperationWorkflowReadinessNotificationCheck, err error) {
	defer func() {
		if err == nil {
			out.Handoff = readinessHandoff(out)
		}
	}()
	out = api.OperationWorkflowReadinessNotificationCheck{Status: "unavailable", CheckedAt: time.Now().UTC()}
	if err := validateReadinessNotificationIdentity(account, tenant, app, scope, id, sequence, operator); err != nil {
		return out, err
	}
	page, err := store.readReadinessInbox(ctx, account, tenant, app, scope, uuid.MustParse(id).String(), sequence-1, 1, operator)
	if err != nil {
		return out, err
	}
	if len(page.Items) != 1 || page.Items[0].Sequence != sequence {
		return out, ErrNotFound
	}
	out.Notification = page.Items[0]
	if c := out.Notification.Coordination; c != nil && c.Completion != nil && c.Completion.ExpectedOutcomeCode != "" {
		out.BusinessOutcome, err = readReadinessBusinessOutcome(ctx, store, account, tenant, app, scope, out.Notification, c.Completion.ExpectedOutcomeCode, operator)
		if err != nil {
			return out, err
		}
	}
	saved := readinessNotificationBaseline(out.Notification)
	if saved == nil {
		return out, nil
	}
	app = uuid.MustParse(app).String()
	opts := api.OperationWorkflowAttentionOptions{AppID: app, Scope: scope, TenantID: page.Subscription.TenantID}
	baseline := newReadinessSnapshot(account, tenant, opts, operator)
	baseline.CapturedAt = out.Notification.CreatedAt
	baseline.Items = append(baseline.Items, *saved)
	req := api.OperationWorkflowReadinessComparisonRequest{Scope: scope, Baseline: baseline, Limit: 1}
	if operator {
		req.TenantID = page.Subscription.TenantID
	} else {
		req.AppID = app
	}
	comparison, err := compareWorkflowReadiness(ctx, store, account, tenant, app, req, operator)
	if err != nil {
		return out, err
	}
	out.CheckedAt = comparison.EvaluatedAt
	if len(comparison.Items) > 0 {
		c := comparison.Items[0]
		out.Change = &c
		out.Status = c.Status
		if c.Current != nil && (c.Status == "unchanged" || c.Status == "changed" || c.Status == "newly_ready" || c.Status == "newly_blocked") {
			out.Status = "blocked"
			if c.Current.Ready {
				out.Status = "ready"
			}
		}
	}
	return out, nil
}

// Verification deliveries carry the source workflow even without a discovery change.
func readinessNotificationBaseline(n api.OperationWorkflowReadinessNotification) *api.OperationWorkflowReadinessSnapshotEntry {
	if n.Change != nil {
		return &n.Change.Baseline
	}
	if n.Verification != nil {
		return &n.Verification.Source
	}
	if n.BusinessOutcome != nil {
		return &n.BusinessOutcome.Source
	}
	if n.EscalationResponse != nil {
		return &n.EscalationResponse.Source
	}
	return nil
}
