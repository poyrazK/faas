package state

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"io"
	"sort"
	"time"
)

type OperationWorkflowOutcomeStore interface {
	ListAccountWorkflowOutcomes(context.Context, string, api.OperationWorkflowOutcomeOptions) (api.OperationWorkflowOutcomesResponse, error)
	ListPlatformTenantWorkflowOutcomes(context.Context, string, string, api.OperationWorkflowOutcomeOptions) (api.OperationWorkflowOutcomesResponse, error)
	SummarizeAccountWorkflowOutcomes(context.Context, string, api.OperationWorkflowOutcomeSummaryOptions) (api.OperationWorkflowOutcomeSummary, error)
	SummarizePlatformTenantWorkflowOutcomes(context.Context, string, string, api.OperationWorkflowOutcomeSummaryOptions) (api.OperationWorkflowOutcomeSummary, error)
}

func outcomeAttentionOptions(opts api.OperationWorkflowOutcomeOptions) api.OperationWorkflowAttentionOptions {
	return api.OperationWorkflowAttentionOptions{AppID: opts.AppID, Scope: opts.Scope, TenantID: opts.TenantID, Workflow: opts.Workflow, BlockerCode: opts.Code, Cursor: opts.Cursor, Limit: opts.Limit}
}
func prepareOperationOutcomes(account, tenant string, opts api.OperationWorkflowOutcomeOptions, operator bool) (api.OperationWorkflowOutcomeOptions, operationAttentionCursor, error) {
	base, c, err := prepareOperationAttention(account, tenant, outcomeAttentionOptions(opts), operator, "workflow-outcomes")
	opts.AppID, opts.TenantID, opts.Limit = base.AppID, base.TenantID, base.Limit
	return opts, c, err
}
func operationOutcomePage(rows []operationAttentionRow, limit int, c operationAttentionCursor) api.OperationWorkflowOutcomesResponse {
	page := operationAttentionPage(rows, limit, c)
	out := api.OperationWorkflowOutcomesResponse{Items: make([]api.OperationWorkflowOutcomeEntry, 0, len(page.Items)), EvaluatedAt: page.EvaluatedAt, NextCursor: page.NextCursor}
	for _, e := range page.Items {
		out.Items = append(out.Items, api.OperationWorkflowOutcomeEntry{AppID: e.AppID, Scope: e.Scope, PlatformTenantID: e.PlatformTenantID, Subject: e.Subject, OperationID: e.OperationID, State: e.State})
	}
	return out
}
func prepareOperationOutcomeSummary(account, tenant string, opts api.OperationWorkflowOutcomeSummaryOptions, operator bool) (api.OperationWorkflowOutcomeSummaryOptions, operationAttentionSummaryCursor, error) {
	rawCursor := opts.Cursor
	opts.Cursor = ""
	base, queue, err := prepareOperationOutcomes(account, tenant, opts.OperationWorkflowOutcomeOptions, operator)
	opts.OperationWorkflowOutcomeOptions = base
	if opts.GroupBy == "" {
		opts.GroupBy = "outcome"
	}
	c := operationAttentionSummaryCursor{Version: 1, Query: queue.Query, GroupBy: opts.GroupBy, EvaluatedAt: queue.EvaluatedAt}
	if err != nil {
		return opts, c, err
	}
	switch opts.GroupBy {
	case "outcome", "workflow":
	case "customer":
		if !operator {
			return opts, c, ErrInvalidArgument
		}
	default:
		return opts, c, ErrInvalidArgument
	}
	if rawCursor == "" {
		return opts, c, nil
	}
	if len(rawCursor) > api.OperationHistoryCursorMaxBytes {
		return opts, c, ErrInvalidArgument
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(rawCursor)
	if err != nil {
		return opts, c, ErrInvalidArgument
	}
	var parsed operationAttentionSummaryCursor
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&parsed) != nil || parsed.Version != 1 || parsed.Query != c.Query || parsed.GroupBy != c.GroupBy || parsed.EvaluatedAt.IsZero() || parsed.EvaluatedAt.After(c.EvaluatedAt.Add(5*time.Second)) || parsed.After == "" || len(parsed.After) > 64 {
		return opts, c, ErrInvalidArgument
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return opts, c, ErrInvalidArgument
	}
	return opts, parsed, nil
}
func finishOperationOutcomeSummary(out api.OperationWorkflowOutcomeSummary, limit int, c operationAttentionSummaryCursor) api.OperationWorkflowOutcomeSummary {
	if out.Groups == nil {
		out.Groups = make([]api.OperationWorkflowOutcomeGroup, 0)
	}
	if len(out.Groups) > limit {
		c.After = out.Groups[limit-1].Value
		raw, _ := json.Marshal(c)
		out.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
		out.Groups = out.Groups[:limit]
	}
	return out
}
func (m *MemStore) ListAccountWorkflowOutcomes(ctx context.Context, account string, opts api.OperationWorkflowOutcomeOptions) (api.OperationWorkflowOutcomesResponse, error) {
	return m.listWorkflowOutcomes(ctx, account, opts.TenantID, opts, true)
}
func (m *MemStore) ListPlatformTenantWorkflowOutcomes(ctx context.Context, account, tenant string, opts api.OperationWorkflowOutcomeOptions) (api.OperationWorkflowOutcomesResponse, error) {
	return m.listWorkflowOutcomes(ctx, account, tenant, opts, false)
}
func (m *MemStore) listWorkflowOutcomes(_ context.Context, account, tenant string, opts api.OperationWorkflowOutcomeOptions, operator bool) (api.OperationWorkflowOutcomesResponse, error) {
	opts, c, err := prepareOperationOutcomes(account, tenant, opts, operator)
	if err != nil {
		return api.OperationWorkflowOutcomesResponse{}, err
	}
	if operator {
		tenant = opts.TenantID
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return operationOutcomePage(m.collectWorkflowAttentionLocked(account, tenant, outcomeAttentionOptions(opts), c, operator, true), opts.Limit, c), nil
}
func (m *MemStore) SummarizeAccountWorkflowOutcomes(ctx context.Context, account string, opts api.OperationWorkflowOutcomeSummaryOptions) (api.OperationWorkflowOutcomeSummary, error) {
	return m.summarizeWorkflowOutcomes(ctx, account, opts.TenantID, opts, true)
}
func (m *MemStore) SummarizePlatformTenantWorkflowOutcomes(ctx context.Context, account, tenant string, opts api.OperationWorkflowOutcomeSummaryOptions) (api.OperationWorkflowOutcomeSummary, error) {
	return m.summarizeWorkflowOutcomes(ctx, account, tenant, opts, false)
}
func (m *MemStore) summarizeWorkflowOutcomes(_ context.Context, account, tenant string, opts api.OperationWorkflowOutcomeSummaryOptions, operator bool) (api.OperationWorkflowOutcomeSummary, error) {
	opts, c, err := prepareOperationOutcomeSummary(account, tenant, opts, operator)
	if err != nil {
		return api.OperationWorkflowOutcomeSummary{}, err
	}
	if operator {
		tenant = opts.TenantID
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := m.collectWorkflowAttentionLocked(account, tenant, outcomeAttentionOptions(opts.OperationWorkflowOutcomeOptions), operationAttentionCursor{EvaluatedAt: c.EvaluatedAt}, operator, true)
	out := api.OperationWorkflowOutcomeSummary{GroupBy: opts.GroupBy, EvaluatedAt: c.EvaluatedAt, WorkflowCount: int64(len(rows)), Groups: make([]api.OperationWorkflowOutcomeGroup, 0)}
	counts := map[string]int64{}
	for _, row := range rows {
		value := row.Entry.State.OutcomeCode
		switch opts.GroupBy {
		case "workflow":
			value = row.Entry.State.Workflow
		case "customer":
			value = row.Entry.PlatformTenantID
		}
		counts[value]++
	}
	for value, count := range counts {
		if value > c.After {
			out.Groups = append(out.Groups, api.OperationWorkflowOutcomeGroup{Value: value, WorkflowCount: count})
		}
	}
	sort.Slice(out.Groups, func(i, j int) bool { return out.Groups[i].Value < out.Groups[j].Value })
	return finishOperationOutcomeSummary(out, opts.Limit, c), nil
}
