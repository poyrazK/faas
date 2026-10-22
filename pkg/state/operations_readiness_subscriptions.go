package state

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type OperationWorkflowReadinessSubscriptionStore interface {
	UpdateWorkflowReadinessNotification(context.Context, string, string, string, string, string, int64, string, api.OperationWorkflowReadinessCoordinationRequest, bool) (api.OperationWorkflowReadinessNotification, error)
	CheckWorkflowReadinessNotification(context.Context, string, string, string, string, string, int64, bool) (api.OperationWorkflowReadinessNotificationCheck, error)
	CreateWorkflowReadinessSubscription(context.Context, string, string, string, api.OperationWorkflowReadinessSubscriptionRequest, bool) (api.OperationWorkflowReadinessSubscription, error)
	ListWorkflowReadinessSubscriptions(context.Context, string, string, string, string, bool) (api.OperationWorkflowReadinessSubscriptionsResponse, error)
	DeleteWorkflowReadinessSubscription(context.Context, string, string, string, string, string, bool) error
	WorkflowReadinessInbox(context.Context, string, string, string, string, string, int64, int, bool) (api.OperationWorkflowReadinessInboxResponse, error)
}
type OperationWorkflowReadinessSubscriptionWorkerStore interface {
	EvaluateDueWorkflowReadinessSubscriptions(context.Context) (int, error)
}
type readinessSubscriptionRecord struct {
	FulfillmentReviewEscalationCursor string                                              `json:"fulfillment_review_escalation_cursor,omitempty"`
	FulfillmentReviewReminderCursor   string                                              `json:"fulfillment_review_reminder_cursor,omitempty"`
	FulfillmentReviewCursor           string                                              `json:"fulfillment_review_cursor,omitempty"`
	ReopeningKnown                    map[string]bool                                     `json:"reopening_known,omitempty"`
	ReopeningCursor                   int64                                               `json:"reopening_cursor,omitempty"`
	TargetObservations                map[string]string                                   `json:"target_observations,omitempty"`
	TargetCursor                      int64                                               `json:"target_cursor,omitempty"`
	DigestDeliveredItems              map[string]api.OperationWorkflowNextActionQueueItem `json:"digest_delivered_items,omitempty"`
	DigestKnown                       map[string]string                                   `json:"digest_known,omitempty"`
	ScheduledActionReviewCursor       int64                                               `json:"scheduled_action_review_cursor,omitempty"`
	EscalationCommitmentCursor        int64                                               `json:"escalation_commitment_cursor,omitempty"`
	HandoffReassignmentCursor         int64                                               `json:"handoff_reassignment_cursor,omitempty"`
	HandoffDeclineCursor              int64                                               `json:"handoff_decline_cursor,omitempty"`
	HandoffDeadlineCursor             int64                                               `json:"handoff_deadline_cursor,omitempty"`
	ReviewDecisionCursor              int64                                               `json:"review_decision_cursor,omitempty"`
	ReviewBlockageCursor              int64                                               `json:"review_blockage_cursor,omitempty"`
	ReviewBlockages                   map[string][]string                                 `json:"review_blockages,omitempty"`
	ReviewResponseCursor              int64                                               `json:"review_response_cursor,omitempty"`
	ImpactSources                     [][]impactReviewSource                              `json:"impact_sources,omitempty"`
	ImpactTruncated                   []bool                                              `json:"impact_truncated,omitempty"`
	PlaybookCursor                    string                                              `json:"playbook_cursor,omitempty"`
	ResponseCursor                    int64                                               `json:"response_cursor,omitempty"`
	OutcomeCursor                     int64                                               `json:"outcome_cursor,omitempty"`
	VerificationCursor                int64                                               `json:"verification_cursor,omitempty"`
	Subscription                      api.OperationWorkflowReadinessSubscription          `json:"subscription"`
	Baseline                          api.OperationWorkflowReadinessSnapshot              `json:"baseline"`
	Seen                              map[string]string                                   `json:"seen"`
	Account                           string                                              `json:"-"`
	Tenant                            string                                              `json:"-"`
	Operator                          bool                                                `json:"-"`
	Lease                             string                                              `json:"-"`
	LeaseUntil                        time.Time                                           `json:"-"`
	NextEvaluation                    time.Time                                           `json:"-"`
}
type readinessSubscriptionBackend interface {
	readinessFollowUpBackend
	readPlaybookProgress(context.Context, workflowPlaybookProgressRecord) ([]api.OperationWorkflowPlaybookProgress, error)
	OperationByID(context.Context, string, string, string) (Operation, error)
	updateReadinessNotification(context.Context, string, string, string, string, string, int64, string, readinessCoordinationUpdate, bool) (api.OperationWorkflowReadinessNotification, error)
	readinessQueueStore
	AppByID(context.Context, string) (App, error)
	GetPlatformTenant(context.Context, string, string) (PlatformTenant, error)
	insertReadinessSubscription(context.Context, readinessSubscriptionRecord) error
	listReadinessSubscriptions(context.Context, string, string, string, string, bool) ([]api.OperationWorkflowReadinessSubscription, error)
	readReadinessInbox(context.Context, string, string, string, string, string, int64, int, bool) (api.OperationWorkflowReadinessInboxResponse, error)
	deleteReadinessSubscription(context.Context, string, string, string, string, string, bool) error
	claimReadinessSubscriptions(context.Context) ([]readinessSubscriptionRecord, error)
	finishReadinessSubscription(context.Context, readinessSubscriptionRecord, []api.OperationWorkflowReadinessNotification, []readinessWatchUpdate) error
}

func readinessSubscriptionOptions(s api.OperationWorkflowReadinessSubscription) api.OperationWorkflowReadinessQueueOptions {
	return api.OperationWorkflowReadinessQueueOptions{AppID: s.AppID, TenantID: s.TenantID, Scope: s.Scope, Workflow: s.Workflow, TargetOperation: s.TargetOperation, Owner: s.Owner, Unassigned: s.Unassigned, Limit: api.OperationHistoryPageMax}
}
func createReadinessSubscription(ctx context.Context, store readinessSubscriptionBackend, account, tenant, app string, req api.OperationWorkflowReadinessSubscriptionRequest, operator bool) (api.OperationWorkflowReadinessSubscription, error) {
	out := api.OperationWorkflowReadinessSubscription{}
	if operator && req.AppID != "" || !operator && req.TenantID != "" || api.ValidateOperationWorkflowName(req.Name) != nil {
		return out, ErrInvalidArgument
	}
	if err := validateImpactWatchRequests(req.BusinessImpactWatches); err != nil {
		return out, err
	}
	if req.NextActionDigest != nil {
		policy, err := normalizeNextActionDigestPolicy(*req.NextActionDigest)
		if err != nil {
			return out, err
		}
		req.NextActionDigest = &policy
	}
	if req.Events == nil {
		req.Events = []string{"newly_ready", "newly_blocked", "reassigned", "unavailable"}
	}
	if len(req.Events) == 0 || !readinessUniqueNames(req.Events, 4, []string{"newly_ready", "newly_blocked", "reassigned", "unavailable"}) {
		return out, ErrInvalidArgument
	}
	out = api.OperationWorkflowReadinessSubscription{NextActionDigest: req.NextActionDigest, ID: uuid.NewString(), AppID: app, TenantID: req.TenantID, Scope: req.Scope, Name: req.Name, Workflow: req.Workflow, TargetOperation: req.TargetOperation, Owner: req.Owner, Unassigned: req.Unassigned, Events: req.Events, Status: "active"}
	opts := readinessSubscriptionOptions(out)
	base, _, err := prepareReadinessQueue(account, tenant, opts, operator)
	if err != nil {
		return out, err
	}
	out.AppID, opts.AppID = base.AppID, base.AppID
	if operator {
		out.TenantID, opts.TenantID = base.TenantID, base.TenantID
	} else {
		tenant = uuid.MustParse(tenant).String()
	}
	owned, err := store.AppByID(ctx, base.AppID)
	if err != nil || !sameOperationHistoryIdentity(owned.AccountID, account) {
		return out, ErrNotFound
	}
	selectedTenant := tenant
	if operator {
		selectedTenant = opts.TenantID
	}
	if selectedTenant != "" {
		if _, err = store.GetPlatformTenant(ctx, account, selectedTenant); err != nil {
			return out, err
		}
	}
	page, err := listReadinessQueue(ctx, store, account, tenant, opts, operator)
	if err != nil {
		return out, err
	}
	out.CreatedAt = page.EvaluatedAt
	if out.NextActionDigest != nil {
		next := out.CreatedAt.Add(time.Duration(out.NextActionDigest.CadenceSeconds) * time.Second)
		out.DigestNextAt = &next
	}
	out.EvaluatedAt = page.EvaluatedAt
	out.WatchedTransitionCount = len(page.Snapshot.Items)
	out.CoverageIncomplete = readinessSubscriptionCoverage(page)
	record := readinessSubscriptionRecord{Subscription: out, Baseline: page.Snapshot, Seen: map[string]string{}, Account: uuid.MustParse(account).String(), Tenant: selectedTenant, Operator: operator, NextEvaluation: time.Now().UTC().Add(api.OperationWorkflowReadinessSubscriptionPollInterval)}
	if len(req.BusinessImpactWatches) > 0 {
		for _, watch := range req.BusinessImpactWatches {
			record.Subscription.BusinessImpactWatches = append(record.Subscription.BusinessImpactWatches, api.OperationWorkflowReadinessImpactWatch{Workflow: watch.Workflow, Kind: watch.Kind, Code: watch.Code, Owner: watch.Owner, AcknowledgeAfterSeconds: watch.AcknowledgeAfterSeconds, FallbackOwner: watch.FallbackOwner})
		}
		if _, err = evaluateReadinessImpactWatches(ctx, store, &record, true); err != nil {
			return api.OperationWorkflowReadinessSubscription{}, err
		}
		out = record.Subscription
	}
	if err = store.insertReadinessSubscription(ctx, record); err != nil {
		return api.OperationWorkflowReadinessSubscription{}, err
	}
	return out, nil
}
func readinessSubscriptionCoverage(p api.OperationWorkflowReadinessQueueResponse) bool {
	for _, row := range p.Snapshot.Items {
		if !row.EvidenceComplete {
			return true
		}
	}
	return p.WorkflowSampleTruncated || p.TransitionSampleTruncated || p.HasMore || p.ExcludedChangedWorkflowCount > 0
}
func readinessSubscriptionIdentity(account, tenant, app, scope, id string, operator bool) error {
	_, _, err := prepareOperationHistoryQuery(account, tenant, api.OperationListOptions{AppID: app, Scope: scope}, operator)
	if err != nil {
		return err
	}
	if id != "" {
		u, e := uuid.Parse(id)
		if e != nil || u == uuid.Nil {
			return ErrInvalidArgument
		}
	}
	return nil
}
func listReadinessSubscriptions(ctx context.Context, store readinessSubscriptionBackend, account, tenant, app, scope string, operator bool) (api.OperationWorkflowReadinessSubscriptionsResponse, error) {
	out := api.OperationWorkflowReadinessSubscriptionsResponse{Items: []api.OperationWorkflowReadinessSubscription{}}
	if err := readinessSubscriptionIdentity(account, tenant, app, scope, "", operator); err != nil {
		return out, err
	}
	items, err := store.listReadinessSubscriptions(ctx, account, tenant, app, scope, operator)
	out.Items = items
	return out, err
}
func deleteReadinessSubscription(ctx context.Context, store readinessSubscriptionBackend, account, tenant, app, scope, id string, operator bool) error {
	if id == "" {
		return ErrInvalidArgument
	}
	if err := readinessSubscriptionIdentity(account, tenant, app, scope, id, operator); err != nil {
		return err
	}
	return store.deleteReadinessSubscription(ctx, account, tenant, app, scope, uuid.MustParse(id).String(), operator)
}
func readinessSubscriptionInbox(ctx context.Context, store readinessSubscriptionBackend, account, tenant, app, scope, id string, after int64, limit int, operator bool) (api.OperationWorkflowReadinessInboxResponse, error) {
	if id == "" {
		return api.OperationWorkflowReadinessInboxResponse{}, ErrInvalidArgument
	}
	if err := readinessSubscriptionIdentity(account, tenant, app, scope, id, operator); err != nil {
		return api.OperationWorkflowReadinessInboxResponse{}, err
	}
	if limit == 0 {
		limit = api.OperationHistoryPageDefault
	}
	if after < 0 || after > 9007199254740991 || limit < 1 || limit > api.OperationHistoryPageMax {
		return api.OperationWorkflowReadinessInboxResponse{}, ErrInvalidArgument
	}
	return store.readReadinessInbox(ctx, account, tenant, app, scope, uuid.MustParse(id).String(), after, limit, operator)
}
func readinessInboxPage(subscription api.OperationWorkflowReadinessSubscription, items []api.OperationWorkflowReadinessNotification, after int64) api.OperationWorkflowReadinessInboxResponse {
	out := api.OperationWorkflowReadinessInboxResponse{Subscription: subscription, Items: items, LatestSequence: subscription.LatestSequence, NextSequence: after}
	first := subscription.LatestSequence - int64(api.OperationWorkflowReadinessInboxMax) + 1
	out.ResyncRequired = after < first-1 || after > subscription.LatestSequence
	if len(items) > 0 {
		out.NextSequence = items[len(items)-1].Sequence
	} else if out.ResyncRequired {
		out.NextSequence = subscription.LatestSequence
	}
	out.HasMore = out.NextSequence < out.LatestSequence
	return out
}
func readinessSubscriptionChangeKind(c api.OperationWorkflowReadinessChange, events []string) string {
	if (c.Status == "newly_ready" || c.Status == "newly_blocked") && slices.Contains(events, c.Status) {
		return c.Status
	}
	if !slices.Contains([]string{"changed", "unchanged", "newly_ready", "newly_blocked"}, c.Status) {
		return "unavailable"
	}
	for _, d := range c.EvidenceChanges {
		if d.Kind == "reassigned" {
			return "reassigned"
		}
	}
	for _, d := range c.EvidenceChanges {
		if d.Kind == "unavailable" || d.After != nil && d.After.Status == "unknown" {
			return "unavailable"
		}
	}
	if !c.EvidenceComparisonComplete && (slices.Contains(c.ChangedCategories, "blockers") || slices.Contains(c.ChangedCategories, "verification")) {
		return "unavailable"
	}
	return ""
}
func readinessSubscriptionNotify(record *readinessSubscriptionRecord, kind, explanation string, change *api.OperationWorkflowReadinessChange) api.OperationWorkflowReadinessNotification {
	record.Subscription.LatestSequence++
	return api.OperationWorkflowReadinessNotification{ID: uuid.NewString(), Sequence: record.Subscription.LatestSequence, Kind: kind, CreatedAt: time.Now().UTC().Truncate(time.Microsecond), Explanation: explanation, Change: change}
}
func evaluateReadinessSubscription(ctx context.Context, store readinessSubscriptionBackend, record *readinessSubscriptionRecord) ([]api.OperationWorkflowReadinessNotification, error) {
	page, err := listReadinessQueue(ctx, store, record.Account, record.Tenant, readinessSubscriptionOptions(record.Subscription), record.Operator)
	if err != nil {
		return nil, err
	}
	req := api.OperationWorkflowReadinessComparisonRequest{Scope: record.Subscription.Scope, Baseline: record.Baseline, Limit: api.OperationHistoryPageMax}
	if record.Operator {
		req.TenantID = record.Subscription.TenantID
	} else {
		req.AppID = record.Subscription.AppID
	}
	comparison, err := compareWorkflowReadiness(ctx, store, record.Account, record.Tenant, record.Subscription.AppID, req, record.Operator)
	if err != nil {
		return nil, err
	}
	record.Subscription.CoverageIncomplete = readinessSubscriptionCoverage(page)
	notifications := []api.OperationWorkflowReadinessNotification{}
	seen := map[string]string{}
	// Compare watched edges even when an owner selector no longer matches, so reassignments remain observable.
	for _, change := range comparison.Items {
		kind := readinessSubscriptionChangeKind(change, record.Subscription.Events)
		key := readinessSnapshotKey(change.Baseline)
		if kind == "unavailable" {
			digest := readinessDigest([]any{change.Status, change.CurrentState, change.EvidenceChanges})
			if change.Current != nil {
				for _, row := range comparison.Snapshot.Items {
					if readinessSnapshotKey(row) == key {
						digest = readinessDigest([]any{change.Status, change.CurrentState, row.RequirementsFingerprint, row.BlockersFingerprint, row.PrerequisitesFingerprint, row.VerificationsFingerprint, change.EvidenceChanges})
						break
					}
				}
			}
			seen[key] = digest
			if record.Seen[key] == digest {
				continue
			}
		}
		if kind != "" && slices.Contains(record.Subscription.Events, kind) {
			explanation := change.Explanation
			if kind == "unavailable" && slices.Contains([]string{"changed", "unchanged", "newly_ready", "newly_blocked"}, change.Status) {
				explanation = "Evidence changed or became unavailable; retained observations cannot fully explain the change. Missing proof does not establish completion or resolution."
			}
			notifications = append(notifications, readinessSubscriptionNotify(record, kind, explanation, &change))
		}
	}
	// Discovery initializes newly observed edges without inventing a previous readiness state.
	// Existing comparisons take precedence over the earlier queue read.
	next := page.Snapshot
	next.Items = []api.OperationWorkflowReadinessSnapshotEntry{}
	keys := map[string]bool{}
	appendEntry := func(row api.OperationWorkflowReadinessSnapshotEntry) {
		key := readinessSnapshotKey(row)
		if keys[key] {
			return
		}
		if len(next.Items) >= api.OperationHistoryPageMax {
			record.Subscription.CoverageIncomplete = true
			return
		}
		keys[key] = true
		next.Items = append(next.Items, row)
	}
	refreshed := map[string]api.OperationWorkflowReadinessSnapshotEntry{}
	matchedOwner := map[string]bool{}
	for _, change := range comparison.Items {
		if change.Current != nil {
			matchedOwner[readinessSnapshotKey(change.Baseline)] = readinessSubscriptionOwnerMatches(record.Subscription, *change.Current)
		}
	}
	for _, row := range comparison.Snapshot.Items {
		refreshed[readinessSnapshotKey(row)] = row
	}
	unavailable := map[string]string{}
	for _, change := range comparison.Items {
		if change.Current == nil {
			unavailable[readinessSnapshotKey(change.Baseline)] = change.Status
		}
	}
	for _, row := range page.Snapshot.Items {
		key := readinessSnapshotKey(row)
		if current, ok := refreshed[key]; ok {
			if matchedOwner[key] {
				appendEntry(current)
			}
		} else if _, known := unavailable[key]; !known {
			appendEntry(row)
		}
	}
	for _, change := range comparison.Items {
		key := readinessSnapshotKey(change.Baseline)
		if current, ok := refreshed[key]; ok {
			if change.Current != nil && readinessSubscriptionOwnerMatches(record.Subscription, *change.Current) {
				appendEntry(current)
			}
		} else if change.Status == "state_unknown" || change.Status == "baseline_ahead" {
			appendEntry(change.Baseline)
		}
	}
	next.CapturedAt = comparison.EvaluatedAt
	record.Baseline = next
	record.Seen = seen
	record.Subscription.Status = "active"
	record.Subscription.EvaluatedAt = comparison.EvaluatedAt
	record.Subscription.WatchedTransitionCount = len(next.Items)
	for _, row := range next.Items {
		if !row.EvidenceComplete {
			record.Subscription.CoverageIncomplete = true
		}
	}
	return notifications, nil
}
func readinessSubscriptionOwnerMatches(s api.OperationWorkflowReadinessSubscription, row api.OperationWorkflowReadinessQueueEntry) bool {
	return (s.Owner == "" || slices.Contains(row.Owners, s.Owner)) && (!s.Unassigned || row.Unassigned)
}
func evaluateDueReadinessSubscriptions(ctx context.Context, store readinessSubscriptionBackend) (int, error) {
	records, err := store.claimReadinessSubscriptions(ctx)
	if err != nil {
		return 0, err
	}
	completed := 0
	for _, record := range records {
		if err = ctx.Err(); err != nil {
			return completed, err
		}
		check, cancel := context.WithTimeout(ctx, api.OperationWorkflowReadinessSubscriptionEvaluationTimeout)
		watchUpdates, watchErr := evaluateReadinessVerificationWatches(check, store, &record)
		cancel()
		check, cancel = context.WithTimeout(ctx, api.OperationWorkflowReadinessSubscriptionEvaluationTimeout)
		outcomeUpdates, outcomeErr := evaluateReadinessOutcomeWatches(check, store, &record)
		watchUpdates = append(watchUpdates, outcomeUpdates...)
		cancel()
		check, cancel = context.WithTimeout(ctx, api.OperationWorkflowReadinessSubscriptionEvaluationTimeout)
		playbookUpdates, playbookErr := evaluatePlaybookEvidenceWatches(check, store, &record)
		watchUpdates = append(watchUpdates, playbookUpdates...)
		cancel()
		check, cancel = context.WithTimeout(ctx, api.OperationWorkflowReadinessSubscriptionEvaluationTimeout)
		blockageUpdates, blockageErr := evaluateReadinessReviewBlockages(check, store, &record)
		watchUpdates = append(watchUpdates, blockageUpdates...)
		cancel()
		check, cancel = context.WithTimeout(ctx, api.OperationWorkflowReadinessSubscriptionEvaluationTimeout)
		scheduledUpdates, scheduledErr := evaluateScheduledActionReviews(check, store, &record)
		watchUpdates = append(watchUpdates, scheduledUpdates...)
		cancel()
		check, cancel = context.WithTimeout(ctx, api.OperationWorkflowReadinessSubscriptionEvaluationTimeout)
		notifications, evaluationErr := evaluateReadinessSubscription(check, store, &record)
		cancel()
		if evaluationErr != nil {
			record.Subscription.Status = "retrying"
			if record.Seen["evaluation"] != "unavailable" && slices.Contains(record.Subscription.Events, "unavailable") {
				notifications = []api.OperationWorkflowReadinessNotification{readinessSubscriptionNotify(&record, "unavailable", "Readiness evaluation is unavailable; no requirement resolution is inferred.", nil)}
			}
			record.Seen["evaluation"] = "unavailable"
		}
		check, cancel = context.WithTimeout(ctx, api.OperationWorkflowReadinessSubscriptionEvaluationTimeout)
		impactNotices, impactErr := evaluateReadinessImpactWatches(check, store, &record, false)
		cancel()
		notifications = append(notifications, impactNotices...)
		check, cancel = context.WithTimeout(ctx, api.OperationWorkflowReadinessSubscriptionEvaluationTimeout)
		digestNotices, digestErr := evaluateNextActionDigest(check, store, &record)
		notifications = append(notifications, digestNotices...)
		cancel()
		check, cancel = context.WithTimeout(ctx, api.OperationWorkflowReadinessSubscriptionEvaluationTimeout)
		targetUpdates, targetErr := evaluateObligationTargets(check, store, &record)
		watchUpdates = append(watchUpdates, targetUpdates...)
		cancel()
		check, cancel = context.WithTimeout(ctx, api.OperationWorkflowReadinessSubscriptionEvaluationTimeout)
		reopeningUpdates, reopeningErr := evaluateObligationReopenings(check, store, &record)
		watchUpdates = append(watchUpdates, reopeningUpdates...)
		cancel()
		check, cancel = context.WithTimeout(ctx, api.OperationWorkflowReadinessSubscriptionEvaluationTimeout)
		fulfillmentUpdates, fulfillmentErr := evaluateFulfillmentReviews(check, store, &record)
		watchUpdates = append(watchUpdates, fulfillmentUpdates...)
		cancel()
		check, cancel = context.WithTimeout(ctx, api.OperationWorkflowReadinessSubscriptionEvaluationTimeout)
		reminderUpdates, reminderErr := evaluateFulfillmentReviewReminders(check, store, &record)
		watchUpdates = append(watchUpdates, reminderUpdates...)
		cancel()

		check, cancel = context.WithTimeout(ctx, api.OperationWorkflowReadinessSubscriptionEvaluationTimeout)
		escalationUpdates, escalationErr := evaluateFulfillmentReviewEscalations(check, store, &record)
		watchUpdates = append(watchUpdates, escalationUpdates...)
		cancel()
		if escalationErr != nil || reminderErr != nil || fulfillmentErr != nil || reopeningErr != nil || targetErr != nil || digestErr != nil || scheduledErr != nil || watchErr != nil || outcomeErr != nil || playbookErr != nil || impactErr != nil || blockageErr != nil {
			record.Subscription.Status = "retrying"
		}
		record.NextEvaluation = time.Now().UTC().Add(api.OperationWorkflowReadinessSubscriptionPollInterval)
		finish, cancel := context.WithTimeout(ctx, api.OperationStreamReleaseTimeout)
		err = store.finishReadinessSubscription(finish, record, notifications, watchUpdates)
		cancel()
		if err != nil {
			return completed, err
		}
		completed++
	}
	return completed, nil
}
func cloneReadinessRecord(r readinessSubscriptionRecord) readinessSubscriptionRecord {
	raw, _ := json.Marshal(r)
	var out readinessSubscriptionRecord
	_ = json.Unmarshal(raw, &out)
	out.Account, out.Tenant, out.Operator, out.Lease, out.LeaseUntil, out.NextEvaluation = r.Account, r.Tenant, r.Operator, r.Lease, r.LeaseUntil, r.NextEvaluation
	return out
}

func cloneReadinessInbox(in api.OperationWorkflowReadinessInboxResponse) api.OperationWorkflowReadinessInboxResponse {
	raw, _ := json.Marshal(in)
	var out api.OperationWorkflowReadinessInboxResponse
	_ = json.Unmarshal(raw, &out)
	return out
}
