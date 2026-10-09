package state

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type OperationWorkflowAttentionSummaryStore interface {
	SummarizeAccountWorkflowAttention(context.Context, string, api.OperationWorkflowAttentionSummaryOptions) (api.OperationWorkflowAttentionSummary, error)
	SummarizePlatformTenantWorkflowAttention(context.Context, string, string, api.OperationWorkflowAttentionSummaryOptions) (api.OperationWorkflowAttentionSummary, error)
}
type operationAttentionSummaryCursor struct {
	Version     int       `json:"v"`
	Query       string    `json:"q"`
	GroupBy     string    `json:"by"`
	EvaluatedAt time.Time `json:"at"`
	After       string    `json:"after"`
}

func prepareOperationAttentionSummary(account, tenant string, opts api.OperationWorkflowAttentionSummaryOptions, operator bool) (api.OperationWorkflowAttentionSummaryOptions, operationAttentionSummaryCursor, error) {
	rawCursor := opts.Cursor
	opts.Cursor = ""
	normalized, queue, err := prepareOperationAttention(account, tenant, opts.OperationWorkflowAttentionOptions, operator)
	opts.OperationWorkflowAttentionOptions = normalized
	if opts.GroupBy == "" {
		opts.GroupBy = "workflow"
	}
	c := operationAttentionSummaryCursor{Version: 1, Query: queue.Query, GroupBy: opts.GroupBy, EvaluatedAt: queue.EvaluatedAt}
	if err != nil {
		return opts, c, err
	}
	switch opts.GroupBy {
	case "workflow", "blocker_code", "target_operation", "dependency_status", "required_outcome_code":
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
func finishOperationAttentionSummary(out api.OperationWorkflowAttentionSummary, limit int, c operationAttentionSummaryCursor) api.OperationWorkflowAttentionSummary {
	if out.Groups == nil {
		out.Groups = make([]api.OperationWorkflowAttentionGroup, 0)
	}
	if len(out.Groups) > limit {
		c.After = out.Groups[limit-1].Value
		raw, _ := json.Marshal(c)
		out.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
		out.Groups = out.Groups[:limit]
	}
	return out
}
func addOperationAttentionStats(stats *api.OperationWorkflowAttentionStats, state api.OperationWorkflowState, blockers []api.OperationWorkflowBlocker, at time.Time) {
	stats.WorkflowCount++
	if len(blockers) > 0 {
		stats.BlockedWorkflowCount++
	}
	if state.Stale {
		stats.StaleWorkflowCount++
	}
	if state.Overdue {
		stats.OverdueWorkflowCount++
		due, err := time.Parse(time.RFC3339Nano, state.DeadlineAt)
		if err == nil && (stats.EarliestOverdueDeadlineAt == nil || due.Before(*stats.EarliestOverdueDeadlineAt)) {
			due = due.UTC()
			stats.EarliestOverdueDeadlineAt = &due
			seconds := state.OverdueSeconds
			stats.LongestOverdueSeconds = &seconds
		}
	}
	stats.BlockerCount += int64(len(blockers))
	for _, b := range blockers {
		first, err := time.Parse(time.RFC3339Nano, b.FirstObservedAt)
		if err != nil {
			stats.UnknownAgeBlockers++
			continue
		}
		if stats.OldestBlockerAt == nil || first.Before(*stats.OldestBlockerAt) {
			first = first.UTC()
			stats.OldestBlockerAt = &first
			age := at.Unix() - first.Unix()
			if at.Nanosecond() < first.Nanosecond() {
				age--
			}
			if age < 0 {
				age = 0
			}
			stats.OldestBlockerAgeSeconds = &age
		}
	}
}
func (m *MemStore) SummarizeAccountWorkflowAttention(ctx context.Context, account string, opts api.OperationWorkflowAttentionSummaryOptions) (api.OperationWorkflowAttentionSummary, error) {
	return m.summarizeWorkflowAttention(ctx, account, opts.TenantID, opts, true)
}
func (m *MemStore) SummarizePlatformTenantWorkflowAttention(ctx context.Context, account, tenant string, opts api.OperationWorkflowAttentionSummaryOptions) (api.OperationWorkflowAttentionSummary, error) {
	return m.summarizeWorkflowAttention(ctx, account, tenant, opts, false)
}
func (m *MemStore) summarizeWorkflowAttention(_ context.Context, account, tenant string, opts api.OperationWorkflowAttentionSummaryOptions, operator bool) (api.OperationWorkflowAttentionSummary, error) {
	opts, c, err := prepareOperationAttentionSummary(account, tenant, opts, operator)
	if err != nil {
		return api.OperationWorkflowAttentionSummary{}, err
	}
	if operator {
		tenant = opts.TenantID
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := m.collectWorkflowAttentionLocked(account, tenant, opts.OperationWorkflowAttentionOptions, operationAttentionCursor{EvaluatedAt: c.EvaluatedAt}, operator, false)
	out := api.OperationWorkflowAttentionSummary{GroupBy: opts.GroupBy, EvaluatedAt: c.EvaluatedAt, Groups: make([]api.OperationWorkflowAttentionGroup, 0)}
	groups := map[string]*api.OperationWorkflowAttentionStats{}
	for _, row := range rows {
		state := row.Entry.State
		addOperationAttentionStats(&out.Totals, state, state.Blockers, c.EvaluatedAt)
		addOperationDependencyStats(&out.Totals, row.Entry.DependencyAttention)
		values := map[string]bool{}
		switch opts.GroupBy {
		case "dependency_status", "required_outcome_code":
			for _, r := range row.Entry.DependencyAttention {
				if !operationAttentionDependencyMatches([]api.OperationWorkflowRelatedInstance{r}, opts.OperationWorkflowAttentionOptions) {
					continue
				}
				value := r.Status
				if opts.GroupBy == "required_outcome_code" {
					value = r.Dependency.RequiredOutcomeCode
				}
				if value != "" {
					values[value] = true
				}
			}
		case "workflow":
			values[state.Workflow] = true
		case "customer":
			values[row.Entry.PlatformTenantID] = true
		case "blocker_code":
			for _, b := range state.Blockers {
				if opts.BlockerCode == "" || opts.BlockerCode == b.Code {
					values[b.Code] = true
				}
			}
		case "target_operation":
			for _, target := range row.Targets {
				if opts.TargetOperation == "" || opts.TargetOperation == target {
					values[target] = true
				}
			}
		}
		for value := range values {
			stats := groups[value]
			if stats == nil {
				stats = &api.OperationWorkflowAttentionStats{}
				groups[value] = stats
			}
			blockers := state.Blockers
			if opts.GroupBy == "blocker_code" || opts.GroupBy == "target_operation" {
				blockers = nil
				for _, b := range state.Blockers {
					if opts.GroupBy == "blocker_code" && b.Code == value || opts.GroupBy == "target_operation" && b.Operation == value {
						blockers = append(blockers, b)
					}
				}
			}
			addOperationAttentionStats(stats, state, blockers, c.EvaluatedAt)
			dependencies := row.Entry.DependencyAttention
			if opts.GroupBy == "dependency_status" || opts.GroupBy == "required_outcome_code" {
				dependencies = nil
				for _, r := range row.Entry.DependencyAttention {
					if operationAttentionDependencyMatches([]api.OperationWorkflowRelatedInstance{r}, opts.OperationWorkflowAttentionOptions) && (opts.GroupBy == "dependency_status" && r.Status == value || opts.GroupBy == "required_outcome_code" && r.Dependency.RequiredOutcomeCode == value) {
						dependencies = append(dependencies, r)
					}
				}
			}
			addOperationDependencyStats(stats, dependencies)
		}
	}
	for value, stats := range groups {
		if value > c.After {
			out.Groups = append(out.Groups, api.OperationWorkflowAttentionGroup{Value: value, Stats: *stats})
		}
	}
	sort.Slice(out.Groups, func(i, j int) bool { return out.Groups[i].Value < out.Groups[j].Value })
	return finishOperationAttentionSummary(out, opts.Limit, c), nil
}
