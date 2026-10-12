package state

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type OperationWorkflowPlaybookEvidenceWatchStore interface {
	ConfigureWorkflowPlaybookEvidenceWatch(context.Context, string, string, api.OperationWorkflowCoordinationTimelineOptions, string, string, api.OperationWorkflowPlaybookEvidenceWatchRequest, bool) (api.OperationWorkflowPlaybookEvidenceWatch, error)
}
type playbookEvidenceWatchBackend interface {
	workflowCaseBackend
	savePlaybookEvidenceWatch(context.Context, workflowPlaybookProgressRecord, api.OperationWorkflowPlaybookEvidenceWatchRequest, api.OperationWorkflowPlaybookEvidenceWatch, bool) (api.OperationWorkflowPlaybookEvidenceWatch, error)
}

func (m *MemStore) ConfigureWorkflowPlaybookEvidenceWatch(ctx context.Context, a, t string, o api.OperationWorkflowCoordinationTimelineOptions, id, actor string, req api.OperationWorkflowPlaybookEvidenceWatchRequest, operator bool) (api.OperationWorkflowPlaybookEvidenceWatch, error) {
	return configurePlaybookEvidenceWatch(ctx, m, a, t, o, id, actor, req, operator)
}
func (s *PgStore) ConfigureWorkflowPlaybookEvidenceWatch(ctx context.Context, a, t string, o api.OperationWorkflowCoordinationTimelineOptions, id, actor string, req api.OperationWorkflowPlaybookEvidenceWatchRequest, operator bool) (api.OperationWorkflowPlaybookEvidenceWatch, error) {
	return configurePlaybookEvidenceWatch(ctx, s, a, t, o, id, actor, req, operator)
}
func configurePlaybookEvidenceWatch(ctx context.Context, store playbookEvidenceWatchBackend, account, tenant string, opts api.OperationWorkflowCoordinationTimelineOptions, id, actor string, req api.OperationWorkflowPlaybookEvidenceWatchRequest, operator bool) (api.OperationWorkflowPlaybookEvidenceWatch, error) {
	out := api.OperationWorkflowPlaybookEvidenceWatch{}
	r, err := preparePlaybookProgressIdentity(ctx, store, account, tenant, opts, operator)
	if err != nil {
		return out, err
	}
	progressID, e1 := uuid.Parse(id)
	subscriptionID, e2 := uuid.Parse(req.SubscriptionID)
	if e1 != nil || e2 != nil || progressID == uuid.Nil || subscriptionID == uuid.Nil || actor == "" || req.ProgressRevision < 1 || req.Revision < 0 || req.Revision >= api.OperationWorkflowReadinessCoordinationRevisionMax || len(req.Owner) > api.OperationWorkflowReadinessFollowUpOwnerMaxBytes || !utf8.ValidString(req.Owner) || strings.TrimSpace(req.Owner) != req.Owner || strings.IndexFunc(req.Owner, unicode.IsControl) >= 0 {
		return out, ErrInvalidArgument
	}
	req.SubscriptionID = subscriptionID.String()
	if req.WatchID != "" {
		parsed, e := uuid.Parse(req.WatchID)
		if e != nil || parsed == uuid.Nil {
			return out, ErrInvalidArgument
		}
		req.WatchID = parsed.String()
	}
	opts.AppID = r.App
	overview, err := workflowCase(ctx, store, r.Account, r.Tenant, opts, operator)
	if err != nil {
		return out, err
	}
	for _, p := range overview.PlaybookProgress {
		if p.ID == progressID.String() {
			r.Progress = p
			break
		}
	}
	if r.Progress.ID == "" {
		return out, ErrNotFound
	}
	if r.Progress.Revision != req.ProgressRevision || req.Enabled && !workflowPlaybookProgressActive(r.Progress) {
		return out, ErrConflict
	}
	if req.Enabled && (len(r.Progress.Playbook.EvidenceRequirements) == 0 && r.Progress.Playbook.ExpectedOutcomeCode == "" || len(playbookEvidenceWatchItems(r.Progress)) != len(r.Progress.Playbook.EvidenceRequirements)) {
		return out, ErrInvalidArgument
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	out = api.OperationWorkflowPlaybookEvidenceWatch{ID: uuid.NewString(), ProgressID: r.Progress.ID, SubscriptionID: req.SubscriptionID, Status: "active", Owner: req.Owner, Subject: api.OperationSubject{Type: r.SubjectType, ID: r.SubjectID}, Workflow: r.Workflow, InstanceID: r.InstanceID, ContractVersion: r.Progress.ContractVersion, PlaybookID: r.Progress.Playbook.ID, PlaybookVersion: r.Progress.Playbook.Version, CreatedAt: now, UpdatedAt: now, UpdatedBy: actor, EvaluatedAt: overview.EvidenceCheckedAt, Items: playbookEvidenceWatchItems(r.Progress), BusinessOutcome: r.Progress.BusinessOutcome}
	guardPlaybookWatchOutcome(&out, out.BusinessOutcome, r.Progress.StartStateRevision)
	if operator {
		out.PlatformTenantID = r.Tenant
	}
	return store.savePlaybookEvidenceWatch(ctx, r, req, out, operator)
}
func playbookEvidenceWatchItems(p api.OperationWorkflowPlaybookProgress) []api.OperationWorkflowPlaybookEvidenceCheck {
	out := []api.OperationWorkflowPlaybookEvidenceCheck{}
	for _, item := range p.Items {
		if item.EvidenceCheck != nil {
			out = append(out, *item.EvidenceCheck)
		}
	}
	return out
}

// Caller holds the subscription and the shared checklist writer lock. Configuration invalidates in-flight leases.
func configurePlaybookEvidenceWatchRecord(r *readinessSubscriptionRecord, identity workflowPlaybookProgressRecord, req api.OperationWorkflowPlaybookEvidenceWatchRequest, w api.OperationWorkflowPlaybookEvidenceWatch) (api.OperationWorkflowPlaybookEvidenceWatch, error) {
	if r.Subscription.Workflow != "" && r.Subscription.Workflow != identity.Workflow || r.Tenant != "" && !sameOperationHistoryIdentity(r.Tenant, identity.Tenant) {
		return w, ErrNotFound
	}
	index := slices.IndexFunc(r.Subscription.PlaybookEvidenceWatches, func(old api.OperationWorkflowPlaybookEvidenceWatch) bool { return old.ProgressID == w.ProgressID })
	if index >= 0 {
		old := r.Subscription.PlaybookEvidenceWatches[index]
		if old.ID != req.WatchID || old.Revision != req.Revision {
			return w, ErrConflict
		}
		w.ID = old.ID
		w.CreatedAt = old.CreatedAt
		if req.Enabled {
			w.OutcomeStateRevision = max(w.OutcomeStateRevision, old.OutcomeStateRevision)
			guardPlaybookWatchOutcome(&w, w.BusinessOutcome, w.OutcomeStateRevision)
		}
		w.Revision = old.Revision + 1
		if !req.Enabled {
			w.Status, w.StopReason = "stopped", "disabled"
			w.Items = old.Items
			w.BusinessOutcome = old.BusinessOutcome
			w.OutcomeStateRevision = old.OutcomeStateRevision
			w.EvaluatedAt = old.EvaluatedAt
		}
		r.Subscription.PlaybookEvidenceWatches[index] = w
	} else {
		if req.WatchID != "" || req.Revision != 0 || !req.Enabled {
			return w, ErrConflict
		}
		if len(r.Subscription.PlaybookEvidenceWatches) >= api.OperationWorkflowPlaybookEvidenceWatchesPerSubscriptionMax {
			stopped := slices.IndexFunc(r.Subscription.PlaybookEvidenceWatches, func(old api.OperationWorkflowPlaybookEvidenceWatch) bool { return old.Status == "stopped" })
			if stopped < 0 {
				return w, NewOperationLimitError("playbook_evidence_watches_per_subscription", api.OperationWorkflowPlaybookEvidenceWatchesPerSubscriptionMax, int64(len(r.Subscription.PlaybookEvidenceWatches)+1))
			}
			r.Subscription.PlaybookEvidenceWatches = slices.Delete(r.Subscription.PlaybookEvidenceWatches, stopped, stopped+1)
		}
		w.Revision = 1
		r.Subscription.PlaybookEvidenceWatches = append(r.Subscription.PlaybookEvidenceWatches, w)
	}
	r.Lease = ""
	r.LeaseUntil = time.Time{}
	r.NextEvaluation = time.Now().UTC()
	return w, nil
}

type playbookEvidenceWatchUpdate struct {
	OutcomeChange    *api.OperationWorkflowPlaybookOutcomeChange
	Watch            api.OperationWorkflowPlaybookEvidenceWatch
	ProgressRevision int64
	Changes          []api.OperationWorkflowPlaybookEvidenceChange
}

func evaluatePlaybookEvidenceWatches(ctx context.Context, store readinessSubscriptionBackend, r *readinessSubscriptionRecord) ([]readinessWatchUpdate, error) {
	candidates := []api.OperationWorkflowPlaybookEvidenceWatch{}
	for _, w := range r.Subscription.PlaybookEvidenceWatches {
		if w.Status == "active" {
			candidates = append(candidates, w)
		}
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	backend, ok := store.(workflowCaseBackend)
	if !ok {
		return nil, fmt.Errorf("playbook case backend unavailable")
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i].ID > r.PlaybookCursor, candidates[j].ID > r.PlaybookCursor
		if a != b {
			return a
		}
		return candidates[i].ID < candidates[j].ID
	})
	if len(candidates) > api.OperationWorkflowPlaybookEvidenceWatchesPerPassMax {
		candidates = candidates[:api.OperationWorkflowPlaybookEvidenceWatchesPerPassMax]
	}
	updates := []readinessWatchUpdate{}
	var firstErr error
	for _, w := range candidates {
		if err := ctx.Err(); err != nil {
			return updates, err
		}
		r.PlaybookCursor = w.ID
		tenant := r.Tenant
		if r.Operator {
			tenant = w.PlatformTenantID
		}
		opts := api.OperationWorkflowCoordinationTimelineOptions{AppID: r.Subscription.AppID, Scope: r.Subscription.Scope, SubjectType: w.Subject.Type, SubjectID: w.Subject.ID, Workflow: w.Workflow, InstanceID: w.InstanceID}
		if r.Operator {
			opts.TenantID = tenant
		}
		overview, err := readPlaybookEvidenceWatchCase(ctx, backend, r.Account, tenant, opts, w.ProgressID, r.Operator)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		u := playbookEvidenceWatchUpdate{Watch: w}
		for _, p := range overview.PlaybookProgress {
			if p.ID != w.ProgressID {
				continue
			}
			u.ProgressRevision = p.Revision
			if !workflowPlaybookProgressActive(p) {
				break
			}
			guardPlaybookWatchOutcome(&u.Watch, p.BusinessOutcome, p.StartStateRevision)
			if w.BusinessOutcome != nil && u.Watch.BusinessOutcome != nil && playbookBusinessOutcomeKey(w.BusinessOutcome) != playbookBusinessOutcomeKey(u.Watch.BusinessOutcome) {
				u.OutcomeChange = &api.OperationWorkflowPlaybookOutcomeChange{BeforeStatus: w.BusinessOutcome.Status, BeforeReason: w.BusinessOutcome.Reason, BeforeReportedOutcomeCode: w.BusinessOutcome.Evidence.ReportedOutcomeCode, After: *u.Watch.BusinessOutcome}
			}
			u.Watch.Items = playbookEvidenceWatchItems(p)
			u.Watch.EvaluatedAt = overview.EvidenceCheckedAt
			for _, current := range u.Watch.Items {
				before := slices.IndexFunc(w.Items, func(item api.OperationWorkflowPlaybookEvidenceCheck) bool {
					return item.Requirement.Label == current.Requirement.Label
				})
				if before >= 0 && (w.Items[before].Status != current.Status || w.Items[before].Reason != current.Reason) {
					u.Changes = append(u.Changes, api.OperationWorkflowPlaybookEvidenceChange{BeforeStatus: w.Items[before].Status, BeforeReason: w.Items[before].Reason, After: current})
				}
			}
			break
		}
		updates = append(updates, readinessWatchUpdate{Playbook: &u})
	}
	return updates, firstErr
}

// Recheck closure/revision under the publication lock; a stale poll never overwrites a new human decision.
func applyPlaybookEvidenceWatchUpdate(r *readinessSubscriptionRecord, u playbookEvidenceWatchUpdate, p *api.OperationWorkflowPlaybookProgress) []api.OperationWorkflowReadinessNotification {
	index := slices.IndexFunc(r.Subscription.PlaybookEvidenceWatches, func(w api.OperationWorkflowPlaybookEvidenceWatch) bool {
		return w.ID == u.Watch.ID && w.Revision == u.Watch.Revision && w.Status == "active"
	})
	if index < 0 {
		return nil
	}
	if p == nil || !workflowPlaybookProgressActive(*p) {
		w := &r.Subscription.PlaybookEvidenceWatches[index]
		w.Status = "stopped"
		w.UpdatedAt = time.Now().UTC().Truncate(time.Microsecond)
		w.UpdatedBy = "system:playbook-evidence-watch"
		w.StopReason = "checklist_closed"
		if p == nil {
			w.StopReason = "checklist_unavailable"
		}
		return nil
	}
	if p.Revision != u.ProgressRevision {
		return nil
	}
	r.Subscription.PlaybookEvidenceWatches[index] = u.Watch
	if len(u.Changes) == 0 && u.OutcomeChange == nil {
		return nil
	}
	w := u.Watch
	n := readinessSubscriptionNotify(r, "playbook_evidence_changed", "Watched checklist evidence changed. Review the reported requirements; original human reviews and application state are preserved.", nil)
	if u.OutcomeChange != nil {
		n.Kind = "playbook_outcome_changed"
		n.Explanation = "The application-reported business result for a watched playbook changed. Human checklist progress is preserved; causation and execution authority are not inferred."
	}
	n.PlaybookEvidence = &api.OperationWorkflowPlaybookEvidenceEvent{WatchID: w.ID, ProgressID: w.ProgressID, PlatformTenantID: w.PlatformTenantID, Subject: w.Subject, Workflow: w.Workflow, InstanceID: w.InstanceID, ContractVersion: w.ContractVersion, PlaybookID: w.PlaybookID, PlaybookVersion: w.PlaybookVersion, CheckedAt: w.EvaluatedAt, Changes: append([]api.OperationWorkflowPlaybookEvidenceChange{}, u.Changes...), OutcomeChange: u.OutcomeChange}
	n.Coordination = &api.OperationWorkflowReadinessCoordination{Revision: 1, Status: "unread", Owner: w.Owner, UpdatedAt: n.CreatedAt, UpdatedBy: "system:playbook-evidence-watch"}
	return []api.OperationWorkflowReadinessNotification{n}
}
func playbookEvidenceWatchIdentity(r readinessSubscriptionRecord, w api.OperationWorkflowPlaybookEvidenceWatch) workflowPlaybookProgressRecord {
	tenant := r.Tenant
	if r.Operator {
		tenant = w.PlatformTenantID
	}
	return workflowPlaybookProgressRecord{Account: r.Account, App: r.Subscription.AppID, Tenant: tenant, Scope: r.Subscription.Scope, SubjectType: w.Subject.Type, SubjectID: w.Subject.ID, Workflow: w.Workflow, InstanceID: w.InstanceID}
}

// Poll only the scoped evidence and watched progress, not the case's inboxes, timeline, or follow-up inventory.
func readPlaybookEvidenceWatchCase(ctx context.Context, store workflowCaseBackend, account, tenant string, opts api.OperationWorkflowCoordinationTimelineOptions, progressID string, operator bool) (api.OperationWorkflowCase, error) {
	out := api.OperationWorkflowCase{}
	identity, err := preparePlaybookProgressIdentity(ctx, store, account, tenant, opts, operator)
	if err != nil {
		return out, err
	}
	out.Timeline.Subject = api.OperationSubject{Type: identity.SubjectType, ID: identity.SubjectID}
	query := api.OperationMilestoneListOptions{AppID: identity.App, Scope: identity.Scope, SubjectType: identity.SubjectType, SubjectID: identity.SubjectID, Workflow: identity.Workflow, WorkflowInstanceID: identity.InstanceID, Limit: 1}
	page, err := store.ListPlatformTenantOperationMilestones(ctx, identity.Account, identity.Tenant, query)
	if err != nil {
		return out, err
	}
	out.EvidenceCheckedAt = time.Now().UTC().Truncate(time.Microsecond)
	out.Instance = page.WorkflowInstance
	if out.Instance != nil && out.Instance.State != nil {
		out.Instance.State.Stale = out.Instance.State.Stale || operationWorkflowStateIsStale(out.EvidenceCheckedAt, *out.Instance.State)
		projectOperationWorkflowReadiness(&page)
	}
	source, err := projectWorkflowCasePlaybooks(ctx, store, identity.Account, identity.Tenant, opts, &out)
	if err != nil {
		return out, err
	}
	progress, err := store.readPlaybookProgress(ctx, identity)
	if err != nil {
		return out, err
	}
	for _, p := range progress {
		if p.ID == progressID {
			out.PlaybookProgress = append(out.PlaybookProgress, p)
			break
		}
	}
	projectWorkflowPlaybookEvidence(&out, source)
	projectWorkflowPlaybookBusinessOutcomes(&out)
	return out, nil
}
