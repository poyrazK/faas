package state

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type OperationWorkflowPlaybookProgressStore interface {
	ReviewWorkflowPlaybookProgress(context.Context, string, string, api.OperationWorkflowCoordinationTimelineOptions, string, string, api.OperationWorkflowPlaybookReviewRequest, bool) (api.OperationWorkflowPlaybookProgress, error)

	StartWorkflowPlaybookProgress(context.Context, string, string, api.OperationWorkflowCoordinationTimelineOptions, string, api.OperationWorkflowPlaybookProgressStartRequest, bool) (api.OperationWorkflowPlaybookProgress, error)
	UpdateWorkflowPlaybookProgress(context.Context, string, string, api.OperationWorkflowCoordinationTimelineOptions, string, string, api.OperationWorkflowPlaybookProgressUpdateRequest, bool) (api.OperationWorkflowPlaybookProgress, error)
}
type workflowPlaybookProgressRecord struct {
	Account, App, Tenant, Scope, SubjectType, SubjectID, Workflow, InstanceID string
	Progress                                                                  api.OperationWorkflowPlaybookProgress
}

func (r workflowPlaybookProgressRecord) key() string {
	return operationWorkflowStateKey(r.Account, r.App, r.Tenant, r.Scope, r.SubjectType, r.SubjectID, r.Workflow, r.InstanceID)
}

type workflowPlaybookProgressBackend interface {
	workflowCaseBackend
	savePlaybookProgressReview(context.Context, workflowPlaybookProgressRecord, int64, string, int64) (api.OperationWorkflowPlaybookProgress, error)
	savePlaybookProgress(context.Context, workflowPlaybookProgressRecord, int64) (api.OperationWorkflowPlaybookProgress, error)
}

func preparePlaybookProgressIdentity(ctx context.Context, store workflowCaseBackend, account, tenant string, opts api.OperationWorkflowCoordinationTimelineOptions, operator bool) (workflowPlaybookProgressRecord, error) {
	r := workflowPlaybookProgressRecord{}
	if operator {
		tenant = opts.TenantID
	} else if opts.TenantID != "" {
		return r, ErrInvalidArgument
	}
	normalized, _, err := prepareOperationHistoryQuery(account, tenant, api.OperationListOptions{AppID: opts.AppID, Scope: opts.Scope}, false)
	if err != nil || opts.Offset != 0 || opts.Limit != 0 || api.ValidateOperationSubject(api.OperationSubject{Type: opts.SubjectType, ID: opts.SubjectID}) != nil || api.ValidateOperationWorkflowName(opts.Workflow) != nil || api.ValidateOperationWorkflowInstanceID(opts.InstanceID) != nil {
		return r, ErrInvalidArgument
	}
	app, err := store.AppByID(ctx, normalized.AppID)
	if err != nil {
		return r, err
	}
	if !sameOperationHistoryIdentity(app.AccountID, account) || app.Status == AppDeleted {
		return r, ErrNotFound
	}
	r = workflowPlaybookProgressRecord{Account: uuid.MustParse(account).String(), App: normalized.AppID, Tenant: canonicalReadinessTenant(tenant), Scope: opts.Scope, SubjectType: opts.SubjectType, SubjectID: opts.SubjectID, Workflow: opts.Workflow, InstanceID: opts.InstanceID}
	if _, err = store.GetPlatformTenant(ctx, r.Account, r.Tenant); err != nil {
		return r, err
	}
	return r, nil
}
func startWorkflowPlaybookProgress(ctx context.Context, store workflowPlaybookProgressBackend, account, tenant string, opts api.OperationWorkflowCoordinationTimelineOptions, actor string, req api.OperationWorkflowPlaybookProgressStartRequest, operator bool) (api.OperationWorkflowPlaybookProgress, error) {
	r, err := preparePlaybookProgressIdentity(ctx, store, account, tenant, opts, operator)
	if err != nil {
		return r.Progress, err
	}
	if actor == "" || api.ValidateOperationWorkflowName(req.PlaybookID) != nil || req.PlaybookVersion < 1 || req.PlaybookVersion > api.OperationWorkflowContractVersionMax || req.ContractVersion < 1 || req.ContractVersion > api.OperationWorkflowContractVersionMax {
		return r.Progress, ErrInvalidArgument
	}
	id, err := uuid.Parse(req.ReportingOperationID)
	if err != nil || id == uuid.Nil {
		return r.Progress, ErrInvalidArgument
	}
	req.ReportingOperationID = id.String()
	// Only the exact current fresh matched rule can start work. Existing progress never upgrades.
	opts.AppID = r.App
	overview, err := workflowCase(ctx, store, r.Account, r.Tenant, opts, operator)
	if err != nil {
		return r.Progress, err
	}
	var matched *api.OperationWorkflowPlaybookGuidance
	for i := range overview.Guidance {
		g := &overview.Guidance[i]
		if g.Playbook.ID == req.PlaybookID && g.Playbook.Version == req.PlaybookVersion && g.ContractVersion == req.ContractVersion && sameOperationHistoryIdentity(g.ReportingOperationID, req.ReportingOperationID) {
			matched = g
			break
		}
	}
	if matched == nil {
		return r.Progress, ErrConflict
	}
	op, err := store.OperationByID(ctx, r.Account, r.Tenant, req.ReportingOperationID)
	if err != nil {
		return r.Progress, err
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	// Stable identity makes a retried start return the original checklist, never reset it.
	r.Progress = api.OperationWorkflowPlaybookProgress{ID: uuid.NewSHA1(uuid.NameSpaceOID, []byte(r.key()+"/"+op.DefinitionID+"/"+matched.Playbook.ID+"/"+fmt.Sprint(matched.Playbook.Version)+"/"+fmt.Sprint(matched.ContractVersion))).String(), Revision: 1, Disposition: "active", Playbook: matched.Playbook, ContractVersion: matched.ContractVersion, DefinitionID: op.DefinitionID, ReportingOperationID: op.ID, Items: []api.OperationWorkflowPlaybookProgressItem{}, History: []api.OperationWorkflowPlaybookProgressChange{}, CreatedAt: now, UpdatedAt: now, CreatedBy: actor}
	if matched.Playbook.ExpectedOutcomeCode != "" && overview.Instance != nil && overview.Instance.State != nil {
		r.Progress.StartStateRevision = overview.Instance.State.Revision
	}
	if matched.Playbook.OutcomeDeadline != nil {
		due := now.Add(time.Duration(matched.Playbook.OutcomeDeadline.AfterSeconds) * time.Second)
		r.Progress.OutcomeDeadlineAt = &due
	}
	for _, label := range matched.Playbook.RequiredEvidence {
		r.Progress.Items = append(r.Progress.Items, api.OperationWorkflowPlaybookProgressItem{Label: label, Status: "pending", Evidence: []api.OperationWorkflowPlaybookEvidence{}})
	}
	return store.savePlaybookProgress(ctx, r, 0)
}
func updateWorkflowPlaybookProgress(ctx context.Context, store workflowPlaybookProgressBackend, account, tenant string, opts api.OperationWorkflowCoordinationTimelineOptions, id, actor string, req api.OperationWorkflowPlaybookProgressUpdateRequest, operator bool) (api.OperationWorkflowPlaybookProgress, error) {
	r, err := preparePlaybookProgressIdentity(ctx, store, account, tenant, opts, operator)
	if err != nil {
		return r.Progress, err
	}
	parsed, err := uuid.Parse(id)
	if err != nil || parsed == uuid.Nil {
		return r.Progress, ErrInvalidArgument
	}
	id = parsed.String()
	if actor == "" || req.Revision < 1 || req.Revision >= api.OperationWorkflowReadinessCoordinationRevisionMax || req.ItemIndex < 0 || (req.Status != "pending" && req.Status != "completed") || strings.TrimSpace(req.Note) == "" || len(req.Note) > api.OperationWorkflowReadinessFollowUpNoteMaxBytes || !utf8.ValidString(req.Note) || strings.IndexFunc(req.Note, unicode.IsControl) >= 0 || req.Evidence == nil || len(req.Evidence) > api.OperationWorkflowReadinessCompletionEvidenceMax {
		return r.Progress, ErrInvalidArgument
	}
	if req.Owner != nil && (req.OutcomeCode == "" || len(*req.Owner) > api.OperationWorkflowReadinessFollowUpOwnerMaxBytes || !utf8.ValidString(*req.Owner) || strings.TrimSpace(*req.Owner) != *req.Owner || strings.IndexFunc(*req.Owner, unicode.IsControl) >= 0) {
		return r.Progress, ErrInvalidArgument
	}
	rows, err := store.readPlaybookProgress(ctx, r)
	if err != nil {
		return r.Progress, err
	}
	found := false
	for _, p := range rows {
		if p.ID == id {
			r.Progress = p
			found = true
			break
		}
	}
	if !found {
		return r.Progress, ErrNotFound
	}
	p := &r.Progress
	if p.Revision != req.Revision || !workflowPlaybookProgressActive(*p) {
		return *p, ErrConflict
	}
	items := &p.Items
	followUpIndex := -1
	newFollowUp := false
	if req.OutcomeCode != "" {
		if api.ValidateOperationWorkflowStateName(req.OutcomeCode) != nil {
			return *p, ErrInvalidArgument
		}
		var steps []string
		for _, guidance := range p.Playbook.OutcomeGuidance {
			if guidance.OutcomeCode == req.OutcomeCode {
				steps = guidance.FollowUpSteps
				break
			}
		}
		if len(steps) == 0 {
			return *p, ErrInvalidArgument
		}
		for i := range p.OutcomeFollowUps {
			if p.OutcomeFollowUps[i].OutcomeCode == req.OutcomeCode {
				followUpIndex = i
				break
			}
		}
		if followUpIndex < 0 {
			// Enrollment captures an advisory fresh exact-case observation before the revision-fenced write.
			opts.AppID = r.App
			overview, e := readPlaybookEvidenceWatchCase(ctx, store, r.Account, r.Tenant, opts, p.ID, operator)
			if e != nil {
				return *p, e
			}
			var observed *api.OperationWorkflowPlaybookBusinessOutcome
			for _, row := range overview.PlaybookProgress {
				if row.ID == p.ID {
					observed = row.BusinessOutcome
					break
				}
			}
			if observed == nil || (observed.Status != "achieved" && observed.Status != "different_outcome") || observed.Evidence.ReportedOutcomeCode != req.OutcomeCode {
				return *p, ErrConflict
			}
			follow := api.OperationWorkflowPlaybookOutcomeFollowUp{OutcomeCode: req.OutcomeCode, Observation: *observed, Items: []api.OperationWorkflowPlaybookProgressItem{}}
			for _, label := range steps {
				follow.Items = append(follow.Items, api.OperationWorkflowPlaybookProgressItem{Label: label, Status: "pending", Evidence: []api.OperationWorkflowPlaybookEvidence{}})
			}
			p.OutcomeFollowUps = append(p.OutcomeFollowUps, follow)
			followUpIndex = len(p.OutcomeFollowUps) - 1
			newFollowUp = true
		}
		items = &p.OutcomeFollowUps[followUpIndex].Items
	}
	if req.ItemIndex >= len(*items) {
		return *p, ErrInvalidArgument
	}
	evidence, err := preparePlaybookProgressEvidence(ctx, store, r, req.Evidence)
	if err != nil {
		return *p, err
	}
	if req.Status == "completed" && followUpIndex >= 0 && playbookStepReadiness(*p, req.OutcomeCode, *items, req.ItemIndex).Status != "ready" {
		return *p, ErrConflict
	}
	if req.Status == "completed" && len(evidence) == 0 {
		return *p, ErrInvalidArgument
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if newFollowUp {
		for _, guidance := range p.Playbook.OutcomeGuidance {
			if guidance.OutcomeCode != req.OutcomeCode {
				continue
			}
			for _, policy := range guidance.FollowUpAssignments {
				for i := range *items {
					if (*items)[i].Label == policy.Label {
						(*items)[i].Owner = policy.Owner
						(*items)[i].ReviewImpact = policy.ReviewImpact
					}
				}
			}
		}
	}
	owner := (*items)[req.ItemIndex].Owner
	if req.Owner != nil {
		owner = *req.Owner
	}
	reopened := followUpIndex >= 0 && (*items)[req.ItemIndex].Status == "completed" && req.Status == "pending"
	item := api.OperationWorkflowPlaybookProgressItem{ReviewImpact: (*items)[req.ItemIndex].ReviewImpact, Owner: owner, DueAt: (*items)[req.ItemIndex].DueAt, Label: (*items)[req.ItemIndex].Label, Status: req.Status, Note: req.Note, Evidence: evidence, ReviewedBy: actor, ReviewedAt: &now}
	(*items)[req.ItemIndex] = item
	p.Revision++
	p.UpdatedAt = now
	if followUpIndex >= 0 {
		// Persist the obligation in the same revision-fenced write as the reopening.
		// Recovery alone cannot validate work performed against the old prerequisites.
		if reopened {
			for i := range *items {
				step := &(*items)[i]
				if step.Status == "completed" && playbookStepDependsOn(*p, req.OutcomeCode, step.Label, item.Label) {
					step.DependencyReviewRevision = p.Revision
					step.DependencyReviewDueAt = nil
					for _, guidance := range p.Playbook.OutcomeGuidance {
						if guidance.OutcomeCode != req.OutcomeCode {
							continue
						}
						for _, policy := range guidance.FollowUpAssignments {
							if policy.Label == step.Label && policy.ReviewAfterSeconds > 0 {
								due := now.Add(time.Duration(policy.ReviewAfterSeconds) * time.Second)
								step.DependencyReviewDueAt = &due
							}
						}
					}
				}
			}
		}
		activateReadyPlaybookStepDeadlines(p, followUpIndex, now)
		follow := &p.OutcomeFollowUps[followUpIndex]
		follow.CompletedCount = 0
		for _, step := range follow.Items {
			if step.Status == "completed" && step.DependencyReviewRevision == 0 {
				follow.CompletedCount++
			}
		}
		follow.ChecklistComplete = len(follow.Items) > 0 && follow.CompletedCount == len(follow.Items)
	}
	p.CompletedCount = 0
	for _, i := range p.Items {
		if i.Status == "completed" {
			p.CompletedCount++
		}
	}
	p.ChecklistComplete = len(p.Items) > 0 && p.CompletedCount == len(p.Items)
	p.History = append(p.History, api.OperationWorkflowPlaybookProgressChange{OutcomeCode: req.OutcomeCode, Revision: p.Revision, ItemIndex: req.ItemIndex, Item: (*items)[req.ItemIndex], At: now, Actor: actor})
	if len(p.History) > api.OperationWorkflowPlaybookProgressHistoryMax {
		p.History = p.History[len(p.History)-api.OperationWorkflowPlaybookProgressHistoryMax:]
		p.HistoryTruncated = true
	}
	return store.savePlaybookProgress(ctx, r, req.Revision)
}
func clonePlaybookProgress(p api.OperationWorkflowPlaybookProgress) api.OperationWorkflowPlaybookProgress {
	p.BusinessOutcome = nil
	p.Items = slices.Clone(p.Items)
	for i := range p.Items {
		p.Items[i].EvidenceCheck = nil
		p.Items[i].Readiness = nil
	}
	p.History = slices.Clone(p.History)
	for i := range p.History {
		p.History[i].Item.EvidenceCheck = nil
		p.History[i].Item.Readiness = nil
	}
	p.OutcomeFollowUps = slices.Clone(p.OutcomeFollowUps)
	for i := range p.OutcomeFollowUps {
		p.OutcomeFollowUps[i].Items = slices.Clone(p.OutcomeFollowUps[i].Items)
		for j := range p.OutcomeFollowUps[i].Items {
			p.OutcomeFollowUps[i].Items[j].Readiness = nil
		}
	}
	raw, _ := json.Marshal(p)
	var out api.OperationWorkflowPlaybookProgress
	_ = json.Unmarshal(raw, &out)
	return out
}
