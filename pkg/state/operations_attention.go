package state

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"io"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

type OperationWorkflowAttentionStore interface {
	ListAccountWorkflowAttention(context.Context, string, api.OperationWorkflowAttentionOptions) (api.OperationWorkflowAttentionResponse, error)
	ListPlatformTenantWorkflowAttention(context.Context, string, string, api.OperationWorkflowAttentionOptions) (api.OperationWorkflowAttentionResponse, error)
}

type operationAttentionCursor struct {
	Sort        string    `json:"sort,omitempty"`
	DeadlineAt  string    `json:"deadline,omitempty"`
	Version     int       `json:"v"`
	Query       string    `json:"q"`
	EvaluatedAt time.Time `json:"at"`
	UpdatedAt   time.Time `json:"after,omitempty"`
	Key         string    `json:"key,omitempty"`
}

func prepareOperationAttention(account, tenant string, opts api.OperationWorkflowAttentionOptions, operator bool, domain ...string) (api.OperationWorkflowAttentionOptions, operationAttentionCursor, error) {
	if operator {
		tenant = opts.TenantID
	}
	normalized, _, err := prepareOperationHistoryQuery(account, tenant, api.OperationListOptions{AppID: opts.AppID, Scope: opts.Scope, TenantID: opts.TenantID, Limit: opts.Limit}, operator)
	if err != nil || len(opts.Cursor) > api.OperationHistoryCursorMaxBytes || opts.Workflow != "" && api.ValidateOperationWorkflowName(opts.Workflow) != nil || opts.TargetOperation != "" && (len(opts.TargetOperation) > api.OperationNameMaxBytes || !operationHistoryName.MatchString(opts.TargetOperation)) {
		return opts, operationAttentionCursor{}, ErrInvalidArgument
	}
	if !operator && opts.TenantID != "" {
		return opts, operationAttentionCursor{}, ErrInvalidArgument
	}
	if opts.Sort == "" {
		opts.Sort = "updated_at"
	}
	if !validAttentionPriority(opts.Priority) || opts.Sort != "updated_at" && opts.Sort != "deadline" {
		return opts, operationAttentionCursor{}, ErrInvalidArgument
	}
	if opts.BlockerCode != "" && (len(opts.BlockerCode) > 64 || !operationHistoryName.MatchString(opts.BlockerCode)) {
		return opts, operationAttentionCursor{}, ErrInvalidArgument
	}
	if opts.Owner != "" && opts.Unassigned || len(opts.Owner) > api.OperationWorkflowBlockerActorMaxBytes || !utf8.ValidString(opts.Owner) || strings.ContainsFunc(opts.Owner, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return opts, operationAttentionCursor{}, ErrInvalidArgument
	}
	if opts.Reason != "" && opts.Reason != "blocked" && opts.Reason != "stale" && opts.Reason != "overdue" && opts.Reason != "dependency" && opts.Reason != "escalated" && opts.Reason != "unacknowledged" && opts.Reason != "follow_up_overdue" && opts.Reason != "awaiting_verification" && opts.Reason != "sla_breached" && opts.Reason != "sla_at_risk" {
		return opts, operationAttentionCursor{}, ErrInvalidArgument
	}
	if opts.DependencyStatus != "" && opts.DependencyStatus != "waiting" && opts.DependencyStatus != "unknown" && opts.DependencyStatus != "outcome_mismatch" || opts.RequiredOutcomeCode != "" && (len(opts.RequiredOutcomeCode) > 64 || !operationHistoryName.MatchString(opts.RequiredOutcomeCode)) {
		return opts, operationAttentionCursor{}, ErrInvalidArgument
	}
	opts.AppID, opts.TenantID, opts.Limit = normalized.AppID, normalized.TenantID, normalized.Limit
	if !operator {
		tenant = uuid.MustParse(tenant).String()
	} else {
		tenant = opts.TenantID
	}
	raw, _ := json.Marshal([]any{uuid.MustParse(account).String(), tenant, opts.AppID, opts.Scope, opts.Workflow, opts.TargetOperation, opts.Reason, operator, opts.BlockerCode, opts.DependencyStatus, opts.RequiredOutcomeCode, opts.Owner, opts.Unassigned})
	if opts.Priority != "" || opts.Sort != "updated_at" {
		raw, _ = json.Marshal([]any{string(raw), opts.Priority, opts.Sort})
	}
	if len(domain) > 0 {
		raw, _ = json.Marshal([]any{string(raw), domain})
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	now := time.Now().UTC().Truncate(time.Microsecond)
	cursor := operationAttentionCursor{Sort: opts.Sort, Version: 1, Query: digest, EvaluatedAt: now}
	if opts.Cursor == "" {
		return opts, cursor, nil
	}
	raw, err = base64.RawURLEncoding.Strict().DecodeString(opts.Cursor)
	if err != nil {
		return opts, cursor, ErrInvalidArgument
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&cursor) != nil {
		return opts, cursor, ErrInvalidArgument
	}
	if cursor.Sort == "" && opts.Sort == "updated_at" {
		cursor.Sort = "updated_at"
	}
	if cursor.Version != 1 || cursor.Sort != opts.Sort || cursor.Query != digest || cursor.EvaluatedAt.IsZero() || cursor.EvaluatedAt.After(now.Add(5*time.Second)) || cursor.UpdatedAt.IsZero() || len(cursor.Key) != 64 || strings.Trim(cursor.Key, "0123456789abcdef") != "" {
		return opts, cursor, ErrInvalidArgument
	}
	if cursor.Sort != "deadline" && cursor.DeadlineAt != "" {
		return opts, cursor, ErrInvalidArgument
	}
	if cursor.DeadlineAt != "" {
		due, err := time.Parse(time.RFC3339Nano, cursor.DeadlineAt)
		if err != nil || due.UTC().Year() < 1 || due.UTC().Year() > 9999 {
			return opts, cursor, ErrInvalidArgument
		}
		cursor.DeadlineAt = due.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano)
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return opts, cursor, ErrInvalidArgument
	}
	return opts, cursor, nil
}

func operationAttentionKey(tenant string, subject api.OperationSubject, workflow, instance string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join([]string{tenant, subject.Type, subject.ID, workflow, instance}, "\x1f"))))
}

func operationAttentionReasons(state api.OperationWorkflowState) []string {
	reasons := make([]string, 0, 2)
	if len(state.Blockers) > 0 {
		reasons = append(reasons, "blocked")
	}
	if state.Overdue {
		reasons = append(reasons, "overdue")
	}
	if state.Stale {
		reasons = append(reasons, "stale")
	}
	if workflowSLAAtRisk(state) {
		reasons = append(reasons, "sla_at_risk")
	}
	if workflowSLABreached(state) {
		reasons = append(reasons, "sla_breached")
	}
	return reasons
}

func operationAttentionTargetMatches(state api.OperationWorkflowState, target string, declarations []operationWorkflowStepDeclaration) bool {
	if target == "" {
		return true
	}
	for _, b := range state.Blockers {
		if b.Operation == target {
			return true
		}
	}
	if !state.Stale && !state.Overdue && !workflowSLARequiresAttention(state) {
		return false
	}
	for _, d := range declarations {
		if d.Operation != target || d.Spec.Workflow != state.Workflow || effectiveWorkflowContractVersion(d.Spec.Version) != state.ContractVersion {
			continue
		}
		for _, edge := range d.Spec.Transitions {
			if edge.From == state.State {
				return true
			}
		}
	}
	return false
}

type operationAttentionRow struct {
	Verifications []api.OperationWorkflowResolutionVerification
	Entry         api.OperationWorkflowAttentionEntry
	Key           string
	Targets       []string
}

func operationAttentionPage(rows []operationAttentionRow, limit int, cursor operationAttentionCursor) api.OperationWorkflowAttentionResponse {
	sort.Slice(rows, func(i, j int) bool {
		if cursor.Sort == "deadline" {
			if compared := compareAttentionDeadline(rows[i].Entry.State.DeadlineAt, rows[j].Entry.State.DeadlineAt); compared != 0 {
				return compared < 0
			}
		}
		if !rows[i].Entry.State.UpdatedAt.Equal(rows[j].Entry.State.UpdatedAt) {
			return rows[i].Entry.State.UpdatedAt.After(rows[j].Entry.State.UpdatedAt)
		}
		return rows[i].Key > rows[j].Key
	})
	page := api.OperationWorkflowAttentionResponse{Items: make([]api.OperationWorkflowAttentionEntry, 0), EvaluatedAt: cursor.EvaluatedAt}
	if len(rows) > limit {
		last := rows[limit-1]
		cursor.UpdatedAt, cursor.Key = last.Entry.State.UpdatedAt, last.Key
		if cursor.Sort == "deadline" {
			cursor.DeadlineAt = last.Entry.State.DeadlineAt
		}
		raw, _ := json.Marshal(cursor)
		page.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
		rows = rows[:limit]
	}
	for _, row := range rows {
		page.Items = append(page.Items, row.Entry)
	}
	return page
}

func (m *MemStore) ListAccountWorkflowAttention(ctx context.Context, account string, opts api.OperationWorkflowAttentionOptions) (api.OperationWorkflowAttentionResponse, error) {
	return m.listWorkflowAttention(ctx, account, opts.TenantID, opts, true)
}
func (m *MemStore) ListPlatformTenantWorkflowAttention(ctx context.Context, account, tenant string, opts api.OperationWorkflowAttentionOptions) (api.OperationWorkflowAttentionResponse, error) {
	return m.listWorkflowAttention(ctx, account, tenant, opts, false)
}
func (m *MemStore) listWorkflowAttention(_ context.Context, account, tenant string, opts api.OperationWorkflowAttentionOptions, operator bool) (api.OperationWorkflowAttentionResponse, error) {
	opts, cursor, err := prepareOperationAttention(account, tenant, opts, operator)
	if err != nil {
		return api.OperationWorkflowAttentionResponse{}, err
	}
	if operator {
		tenant = opts.TenantID
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return operationAttentionPage(m.collectWorkflowAttentionLocked(account, tenant, opts, cursor, operator, false), opts.Limit, cursor), nil
}
func (m *MemStore) collectWorkflowAttentionLocked(account, tenant string, opts api.OperationWorkflowAttentionOptions, cursor operationAttentionCursor, operator bool, outcomes bool) []operationAttentionRow {
	data := m.operationMemoryLocked()
	declarations := make([]operationWorkflowStepDeclaration, 0)
	for _, d := range data.definitions {
		if !sameOperationHistoryIdentity(d.AccountID, account) || !sameOperationHistoryIdentity(d.AppID, opts.AppID) || d.Scope != opts.Scope {
			continue
		}
		for _, step := range d.Spec.WorkflowSteps {
			declarations = append(declarations, operationWorkflowStepDeclaration{Operation: d.Spec.Name, Spec: step})
		}
	}
	rows := make([]operationAttentionRow, 0)
	now := time.Now().UTC()
	for _, record := range data.workflowStates {
		if !sameOperationHistoryIdentity(record.AccountID, account) || !sameOperationHistoryIdentity(record.AppID, opts.AppID) || record.Scope != opts.Scope || tenant != "" && !sameOperationHistoryIdentity(record.TenantID, tenant) || opts.Workflow != "" && record.State.Workflow != opts.Workflow {
			continue
		}
		receipt, ok := data.workflowStateReports[record.OperationID+"/"+record.ReportID]
		if !ok {
			continue
		}
		op, ok := data.operations[receipt.History.OperationID]
		if !ok || !operationRetained(op, now) {
			continue
		}
		state := record.State
		state.Blockers = append([]api.OperationWorkflowBlocker(nil), state.Blockers...)
		state.DependsOn = append([]api.OperationWorkflowDependency(nil), state.DependsOn...)
		state.BlockerResolutions = append([]api.OperationWorkflowBlockerResolution(nil), state.BlockerResolutions...)
		state.EvidenceMilestones = append([]api.OperationWorkflowEvidenceMilestone(nil), state.EvidenceMilestones...)
		state.Stale = operationWorkflowStateIsStale(cursor.EvaluatedAt, state)
		evaluateOperationWorkflowDeadline(cursor.EvaluatedAt, &state)
		budget := workflowStateSLABudget(data.definitions[op.DefinitionID].Spec, state)
		if budget > 0 {
			state.SLA = workflowStateSLA(state, m.workflowBottleneckHistoryAtLocked(account, record.TenantID, opts.AppID, opts.Scope, *op.Subject, state.Workflow, state.InstanceID, cursor.EvaluatedAt, now), budget, workflowStateSLAWarning(data.definitions[op.DefinitionID].Spec, state), cursor.EvaluatedAt)
		}
		escalations := workflowBlockerEscalations(data.definitions[op.DefinitionID].Spec, state, cursor.EvaluatedAt)
		dependencies := []api.OperationWorkflowRelatedInstance(nil)
		if !outcomes && !state.Terminal && len(state.DependsOn) > 0 {
			snapshot := &api.OperationWorkflowInstanceSnapshot{State: &state}
			page := api.OperationMilestonesResponse{WorkflowInstance: snapshot}
			m.projectRelatedWorkflowsLocked(&page, account, record.TenantID, api.OperationMilestoneListOptions{AppID: opts.AppID, Scope: opts.Scope}, false, now)
			dependencies = unresolvedWorkflowDependencies(snapshot.RelatedWorkflows)
		}
		verifications := m.workflowVerificationsLocked(account, record.TenantID, opts.AppID, opts.Scope, api.OperationSubject{Type: record.SubjectType, ID: record.SubjectID}, state.Workflow, state.InstanceID, cursor.EvaluatedAt, now)
		matchingVerifications := selectedVerifications(verifications, opts)
		if !operationAttentionDependencyMatches(dependencies, opts) {
			matchingVerifications = nil
		}
		if outcomes {
			if !state.Terminal || state.OutcomeCode == "" || opts.BlockerCode != "" && state.OutcomeCode != opts.BlockerCode {
				continue
			}
		} else if len(matchingVerifications) == 0 {
			if opts.Reason == "awaiting_verification" {
				continue
			}
			if !workflowFollowUpMatches(state.Blockers, opts, cursor.EvaluatedAt) || len(state.Blockers) == 0 && !state.Stale && !state.Overdue && !workflowSLARequiresAttention(state) && len(dependencies) == 0 || opts.Reason == "dependency" && len(dependencies) == 0 || !operationAttentionDependencyMatches(dependencies, opts) || opts.Reason == "overdue" && !state.Overdue || opts.Reason == "blocked" && len(state.Blockers) == 0 || opts.Reason == "stale" && !state.Stale || opts.Reason == "sla_breached" && !workflowSLABreached(state) || opts.Reason == "sla_at_risk" && !workflowSLAAtRisk(state) || !operationAttentionTargetMatches(state, opts.TargetOperation, declarations) || !operationAttentionCodeMatches(state, opts.BlockerCode) || !operationAttentionOwnerMatches(state.Blockers, opts) || opts.Reason == "escalated" && !workflowEscalationMatches(state.Blockers, escalations, opts) {
				continue
			}
		}
		subject := api.OperationSubject{Type: record.SubjectType, ID: record.SubjectID}
		key := operationAttentionKey(record.TenantID, subject, state.Workflow, state.InstanceID)
		if !attentionAfterCursor(state, key, cursor) {
			continue
		}
		entry := api.OperationWorkflowAttentionEntry{Escalations: escalations, AppID: opts.AppID, Scope: opts.Scope, Subject: subject, OperationID: op.ID, State: state, Reasons: operationAttentionReasons(state), DependencyAttention: dependencies}
		entry.ResolutionVerifications, entry.AwaitingVerificationCount = verificationPreview(verifications, opts)
		entry.ResolutionVerificationCount = int64(len(verifications))
		if entry.AwaitingVerificationCount > 0 {
			entry.Reasons = append(entry.Reasons, "awaiting_verification")
		}
		entry.Reasons = append(entry.Reasons, workflowFollowUpReasons(state, cursor.EvaluatedAt)...)
		if len(escalations) > 0 {
			entry.Reasons = append(entry.Reasons, "escalated")
		}
		if len(dependencies) > 0 {
			entry.Reasons = append(entry.Reasons, "dependency")
		}
		if operator {
			entry.PlatformTenantID = record.TenantID
		}
		targets := map[string]bool{}
		for _, b := range state.Blockers {
			targets[b.Operation] = true
		}
		if state.Stale || state.Overdue || workflowSLARequiresAttention(state) {
			for _, d := range declarations {
				if operationAttentionTargetMatches(state, d.Operation, []operationWorkflowStepDeclaration{d}) {
					targets[d.Operation] = true
				}
			}
		}
		row := operationAttentionRow{Verifications: verifications, Entry: entry, Key: key}
		for target := range targets {
			row.Targets = append(row.Targets, target)
		}
		rows = append(rows, row)
	}
	return rows
}

func operationAttentionCodeMatches(state api.OperationWorkflowState, code string) bool {
	if code == "" {
		return true
	}
	for _, b := range state.Blockers {
		if b.Code == code {
			return true
		}
	}
	return false
}

func operationAttentionBlockerMatches(b api.OperationWorkflowBlocker, opts api.OperationWorkflowAttentionOptions) bool {
	return (opts.Priority == "" || effectiveBlockerPriority(b) == opts.Priority) && (opts.Owner == "" || b.Owner == opts.Owner) && (!opts.Unassigned || b.Owner == "") && (opts.BlockerCode == "" || b.Code == opts.BlockerCode) && (opts.TargetOperation == "" || b.Operation == opts.TargetOperation)
}
func operationAttentionOwnerMatches(blockers []api.OperationWorkflowBlocker, opts api.OperationWorkflowAttentionOptions) bool {
	if opts.Owner == "" && !opts.Unassigned && opts.Priority == "" {
		return true
	}
	for _, b := range blockers {
		if operationAttentionBlockerMatches(b, opts) {
			return true
		}
	}
	return false
}
func operationAttentionSummaryBlockers(blockers []api.OperationWorkflowBlocker, opts api.OperationWorkflowAttentionSummaryOptions, escalations []api.OperationWorkflowBlockerEscalation, at time.Time) []api.OperationWorkflowBlocker {
	if opts.Reason == "awaiting_verification" {
		return nil
	}
	if opts.Owner == "" && !opts.Unassigned && opts.Priority == "" && opts.GroupBy != "owner" && opts.Reason != "escalated" && opts.Reason != "unacknowledged" && opts.Reason != "follow_up_overdue" {
		return blockers
	}
	result := make([]api.OperationWorkflowBlocker, 0, len(blockers))
	for _, b := range blockers {
		if operationAttentionBlockerMatches(b, opts.OperationWorkflowAttentionOptions) && blockerFollowUpMatches(b, opts.Reason, at) && (opts.Reason != "escalated" || workflowBlockerEscalated(b, escalations)) {
			result = append(result, b)
		}
	}
	return result
}
