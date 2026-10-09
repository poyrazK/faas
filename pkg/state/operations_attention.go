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
)

type OperationWorkflowAttentionStore interface {
	ListAccountWorkflowAttention(context.Context, string, api.OperationWorkflowAttentionOptions) (api.OperationWorkflowAttentionResponse, error)
	ListPlatformTenantWorkflowAttention(context.Context, string, string, api.OperationWorkflowAttentionOptions) (api.OperationWorkflowAttentionResponse, error)
}

type operationAttentionCursor struct {
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
	if opts.BlockerCode != "" && (len(opts.BlockerCode) > 64 || !operationHistoryName.MatchString(opts.BlockerCode)) {
		return opts, operationAttentionCursor{}, ErrInvalidArgument
	}
	if opts.Reason != "" && opts.Reason != "blocked" && opts.Reason != "stale" && opts.Reason != "overdue" && opts.Reason != "dependency" {
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
	raw, _ := json.Marshal([]any{uuid.MustParse(account).String(), tenant, opts.AppID, opts.Scope, opts.Workflow, opts.TargetOperation, opts.Reason, operator, opts.BlockerCode, opts.DependencyStatus, opts.RequiredOutcomeCode})
	if len(domain) > 0 {
		raw, _ = json.Marshal([]any{string(raw), domain})
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	now := time.Now().UTC().Truncate(time.Microsecond)
	cursor := operationAttentionCursor{Version: 1, Query: digest, EvaluatedAt: now}
	if opts.Cursor == "" {
		return opts, cursor, nil
	}
	raw, err = base64.RawURLEncoding.Strict().DecodeString(opts.Cursor)
	if err != nil {
		return opts, cursor, ErrInvalidArgument
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&cursor) != nil || cursor.Version != 1 || cursor.Query != digest || cursor.EvaluatedAt.IsZero() || cursor.EvaluatedAt.After(now.Add(5*time.Second)) || cursor.UpdatedAt.IsZero() || len(cursor.Key) != 64 || strings.Trim(cursor.Key, "0123456789abcdef") != "" {
		return opts, cursor, ErrInvalidArgument
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
	if !state.Stale && !state.Overdue {
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
	Entry   api.OperationWorkflowAttentionEntry
	Key     string
	Targets []string
}

func operationAttentionPage(rows []operationAttentionRow, limit int, cursor operationAttentionCursor) api.OperationWorkflowAttentionResponse {
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].Entry.State.UpdatedAt.Equal(rows[j].Entry.State.UpdatedAt) {
			return rows[i].Entry.State.UpdatedAt.After(rows[j].Entry.State.UpdatedAt)
		}
		return rows[i].Key > rows[j].Key
	})
	page := api.OperationWorkflowAttentionResponse{Items: make([]api.OperationWorkflowAttentionEntry, 0), EvaluatedAt: cursor.EvaluatedAt}
	if len(rows) > limit {
		last := rows[limit-1]
		cursor.UpdatedAt, cursor.Key = last.Entry.State.UpdatedAt, last.Key
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
		dependencies := []api.OperationWorkflowRelatedInstance(nil)
		if !outcomes && !state.Terminal && len(state.DependsOn) > 0 {
			snapshot := &api.OperationWorkflowInstanceSnapshot{State: &state}
			page := api.OperationMilestonesResponse{WorkflowInstance: snapshot}
			m.projectRelatedWorkflowsLocked(&page, account, record.TenantID, api.OperationMilestoneListOptions{AppID: opts.AppID, Scope: opts.Scope}, false, now)
			dependencies = unresolvedWorkflowDependencies(snapshot.RelatedWorkflows)
		}
		if outcomes {
			if !state.Terminal || state.OutcomeCode == "" || opts.BlockerCode != "" && state.OutcomeCode != opts.BlockerCode {
				continue
			}
		} else {
			if len(state.Blockers) == 0 && !state.Stale && !state.Overdue && len(dependencies) == 0 || opts.Reason == "dependency" && len(dependencies) == 0 || !operationAttentionDependencyMatches(dependencies, opts) || opts.Reason == "overdue" && !state.Overdue || opts.Reason == "blocked" && len(state.Blockers) == 0 || opts.Reason == "stale" && !state.Stale || !operationAttentionTargetMatches(state, opts.TargetOperation, declarations) || !operationAttentionCodeMatches(state, opts.BlockerCode) {
				continue
			}
		}
		subject := api.OperationSubject{Type: record.SubjectType, ID: record.SubjectID}
		key := operationAttentionKey(record.TenantID, subject, state.Workflow, state.InstanceID)
		if cursor.Key != "" && (state.UpdatedAt.After(cursor.UpdatedAt) || state.UpdatedAt.Equal(cursor.UpdatedAt) && key >= cursor.Key) {
			continue
		}
		entry := api.OperationWorkflowAttentionEntry{AppID: opts.AppID, Scope: opts.Scope, Subject: subject, OperationID: op.ID, State: state, Reasons: operationAttentionReasons(state), DependencyAttention: dependencies}
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
		if state.Stale || state.Overdue {
			for _, d := range declarations {
				if operationAttentionTargetMatches(state, d.Operation, []operationWorkflowStepDeclaration{d}) {
					targets[d.Operation] = true
				}
			}
		}
		row := operationAttentionRow{Entry: entry, Key: key}
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
