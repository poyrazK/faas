package state

import (
	"context"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"sort"
	"time"
)

type WorkflowRecoveryReportStore interface {
	GetWorkflowRecoveryReport(context.Context, string, string, api.WorkflowRecoveryReportOptions) (api.WorkflowRecoveryReport, error)
}

func (m *MemStore) GetWorkflowRecoveryReport(ctx context.Context, account, app string, o api.WorkflowRecoveryReportOptions) (api.WorkflowRecoveryReport, error) {
	h, err := m.getWorkflowRecoveryHistory(ctx, account, app, o.HistoryOptions(), api.WorkflowRecoveryReportEntriesMax+1)
	if err != nil {
		return api.WorkflowRecoveryReport{}, fmt.Errorf("read recovery report: %w", err)
	}
	return recoveryReport(h), nil
}
func (s *PgStore) GetWorkflowRecoveryReport(ctx context.Context, account, app string, o api.WorkflowRecoveryReportOptions) (api.WorkflowRecoveryReport, error) {
	h, err := s.getWorkflowRecoveryHistory(ctx, account, app, o.HistoryOptions(), api.WorkflowRecoveryReportEntriesMax+1)
	if err != nil {
		return api.WorkflowRecoveryReport{}, fmt.Errorf("read recovery report: %w", err)
	}
	return recoveryReport(h), nil
}

type recoveryReportAction struct {
	applied, result *api.WorkflowRecoveryHistoryEntry
}
type recoveryReportGroupKey struct {
	workflow string
	version  int64
}

func reportPreferred(old *api.WorkflowRecoveryHistoryEntry, e api.WorkflowRecoveryHistoryEntry) *api.WorkflowRecoveryHistoryEntry {
	if old == nil || e.Durable && !old.Durable || e.Durable == old.Durable && (e.OccurredAt.After(old.OccurredAt) || e.OccurredAt.Equal(old.OccurredAt) && e.ID > old.ID) {
		return &e
	}
	return old
}
func reportEvidence(entries []api.WorkflowRecoveryHistoryEntry, e api.WorkflowRecoveryHistoryEntry) []api.WorkflowRecoveryHistoryEntry {
	if len(entries) < api.WorkflowRecoveryReportEvidenceMax {
		entries = append(entries, e)
	}
	return entries
}
func recoveryReport(h api.WorkflowRecoveryHistory) api.WorkflowRecoveryReport {
	out := api.WorkflowRecoveryReport{ObservedAt: h.ObservedAt, CreatedAfter: h.CreatedAfter, CreatedBefore: h.CreatedBefore, Groups: []api.WorkflowRecoveryReportGroup{}, Blockers: []api.WorkflowRecoveryReportBlocker{}, Evidence: []api.WorkflowRecoveryHistoryEntry{}, CoverageNotes: []string{
		"Retained evidence only: durable recording is prospective, older events are not backfilled, and retention is 30 days / 10000 durable events per app.",
		"Attempts and results are independently selected by event time. Without observed result means no result in this window, not proof that a child is still running.",
		"Timing measures retry application to result recording for matched evidence, including polling delay; it does not measure original failure to recovery.",
		"Blockers count distinct actions per observed reason, not worker polls or time blocked. Policy pauses without workflow associations are omitted by workflow filters.",
	}}
	out.Truncated = h.NextAfter != "" || len(h.Entries) > api.WorkflowRecoveryReportEntriesMax
	if len(h.Entries) > api.WorkflowRecoveryReportEntriesMax {
		h.Entries = h.Entries[:api.WorkflowRecoveryReportEntriesMax]
	}
	out.ScannedEntries = len(h.Entries)
	if out.Truncated {
		out.CoverageNotes = append(out.CoverageNotes, "Scan limit reached: only the newest retained entries are summarized; totals are partial.")
	}
	actions := map[string]*recoveryReportAction{}
	blockers := map[string]map[string]api.WorkflowRecoveryHistoryEntry{}
	pauses := map[string]api.WorkflowRecoveryHistoryEntry{}
	for _, e := range h.Entries {
		if e.Durable {
			out.DurableEntries++
		}
		if e.Kind == "policy_paused" || e.Kind == "policy_control" && e.Outcome == "pause" {
			key := fmt.Sprintf("%d:%s", e.PolicyVersion, e.OccurredAt.UTC().Format(time.RFC3339Nano))
			pauses[key] = e
		}
		if e.ActionID == "" {
			continue
		}
		a := actions[e.ActionID]
		if a == nil {
			a = &recoveryReportAction{}
			actions[e.ActionID] = a
		}
		reason := e.EvaluationReason
		if reason == "" && e.Kind == "retry_evaluation" {
			reason = e.Reason
		}
		if e.Kind == "retry_applied" || e.Kind == "retry_transition" && reason == "child_created" {
			a.applied = reportPreferred(a.applied, e)
		}
		if e.Kind == "retry_result" || e.Kind == "retry_transition" && reason == "result_recorded" {
			a.result = reportPreferred(a.result, e)
		}
		switch reason {
		case "cooldown", "hourly_budget", "execution_quota", "outstanding_retry", "inspection_error", "evidence_unknown", "admission_wait":
			if blockers[reason] == nil {
				blockers[reason] = map[string]api.WorkflowRecoveryHistoryEntry{}
			}
			if prev, ok := blockers[reason][e.ActionID]; !ok || e.OccurredAt.After(prev.OccurredAt) {
				blockers[reason][e.ActionID] = e
			}
		}
	}
	groups := map[recoveryReportGroupKey]*api.WorkflowRecoveryReportTotals{}
	group := func(e api.WorkflowRecoveryHistoryEntry) *api.WorkflowRecoveryReportTotals {
		key := recoveryReportGroupKey{e.Workflow, e.PolicyVersion}
		if groups[key] == nil {
			groups[key] = &api.WorkflowRecoveryReportTotals{}
		}
		return groups[key]
	}
	// Accumulate duration sums until all samples are counted.
	add := func(t *api.WorkflowRecoveryReportTotals, a *recoveryReportAction) {
		if a.applied != nil {
			t.Attempts++
			if a.result == nil {
				t.WithoutObservedResult++
			}
		}
		if a.result == nil {
			return
		}
		switch a.result.Outcome {
		case "succeeded":
			t.Succeeded++
		case "failed":
			t.Failed++
		case "cancelled":
			t.Cancelled++
		default:
			t.OtherResults++
		}
		if a.applied != nil && a.applied.ChildJobID != "" && a.applied.ChildJobID == a.result.ChildJobID && !a.result.OccurredAt.Before(a.applied.OccurredAt) {
			seconds := a.result.OccurredAt.Sub(a.applied.OccurredAt).Seconds()
			if t.MeanRetryResultSeconds == nil {
				t.MeanRetryResultSeconds = new(float64)
			}
			*t.MeanRetryResultSeconds += seconds
			t.TimedResults++
		}
	}
	ids := make([]string, 0, len(actions))
	for id := range actions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		a := actions[id]
		if a.applied == nil && a.result == nil {
			continue
		}
		e := a.applied
		if e == nil {
			e = a.result
		}
		add(&out.Totals, a)
		add(group(*e), a)
		if a.result != nil {
			out.Evidence = reportEvidence(out.Evidence, *a.result)
		} else {
			out.Evidence = reportEvidence(out.Evidence, *e)
		}
	}
	for _, e := range pauses {
		out.Totals.Pauses++
		group(e).Pauses++
	}
	finish := func(t *api.WorkflowRecoveryReportTotals) {
		if t.TimedResults > 0 {
			*t.MeanRetryResultSeconds /= float64(t.TimedResults)
		}
	}
	finish(&out.Totals)
	for key, t := range groups {
		finish(t)
		out.Groups = append(out.Groups, api.WorkflowRecoveryReportGroup{Workflow: key.workflow, PolicyVersion: key.version, Totals: *t})
	}
	sort.Slice(out.Groups, func(i, j int) bool {
		a, b := out.Groups[i], out.Groups[j]
		if a.Workflow != b.Workflow {
			return a.Workflow < b.Workflow
		}
		return a.PolicyVersion < b.PolicyVersion
	})
	for reason, items := range blockers {
		b := api.WorkflowRecoveryReportBlocker{Reason: reason, Actions: len(items), Evidence: []api.WorkflowRecoveryHistoryEntry{}}
		entries := make([]api.WorkflowRecoveryHistoryEntry, 0, len(items))
		for _, e := range items {
			entries = append(entries, e)
		}
		sort.Slice(entries, func(i, j int) bool {
			if !entries[i].OccurredAt.Equal(entries[j].OccurredAt) {
				return entries[i].OccurredAt.After(entries[j].OccurredAt)
			}
			return entries[i].ID < entries[j].ID
		})
		for _, e := range entries {
			b.Evidence = reportEvidence(b.Evidence, e)
		}
		out.Blockers = append(out.Blockers, b)
	}
	sort.Slice(out.Blockers, func(i, j int) bool {
		a, b := out.Blockers[i], out.Blockers[j]
		if a.Actions != b.Actions {
			return a.Actions > b.Actions
		}
		return a.Reason < b.Reason
	})
	if out.Totals.Succeeded+out.Totals.Failed+out.Totals.Cancelled+out.Totals.OtherResults < api.WorkflowRecoveryReportEvidenceMax {
		out.CoverageNotes = append(out.CoverageNotes, "Small observed result sample; avoid drawing policy effectiveness conclusions from these counts alone.")
	}
	return out
}
