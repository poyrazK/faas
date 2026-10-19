package mcpmanagement

import (
	"github.com/onebox-faas/faas/pkg/api"
)

type wakeHistoryRowView struct {
	WakeID             string `json:"wake_id,omitempty"`
	WakeIDStatus       string `json:"wake_id_status"`
	Kind               string `json:"kind"`
	State              string `json:"state"`
	At                 string `json:"at,omitempty"`
	StartedAtStatus    string `json:"started_at_status"`
	WindowMembership   string `json:"window_membership"`
	Trigger            string `json:"trigger,omitempty"`
	TriggerClass       string `json:"trigger_class,omitempty"`
	Method             string `json:"method,omitempty"`
	Tier               string `json:"tier,omitempty"`
	LabelsModified     bool   `json:"labels_modified"`
	QueuedCount        *int32 `json:"queued_count,omitempty"`
	QueuedCountStatus  string `json:"queued_count_status"`
	ConcurrencyAtAdmit *int32 `json:"concurrency_at_admit,omitempty"`
	ConcurrencyStatus  string `json:"concurrency_status"`
	AtCapacity         *bool  `json:"at_capacity,omitempty"`
	AtCapacityStatus   string `json:"at_capacity_status"`
	ReadyInMS          *int32 `json:"ready_in_ms,omitempty"`
	ReadinessStatus    string `json:"readiness_status"`
}

type wakeHistoryView struct {
	App                     string               `json:"app"`
	AppID                   string               `json:"app_id"`
	Scope                   string               `json:"scope"`
	Status                  string               `json:"status"`
	RequestedSince          string               `json:"requested_since"`
	RequestedUntil          string               `json:"requested_until"`
	WindowVerification      string               `json:"window_verification"`
	WindowBounds            string               `json:"window_bounds"`
	AsOf                    string               `json:"as_of"`
	SourceStatus            string               `json:"source_status"`
	SourceCompleteness      string               `json:"source_completeness"`
	Freshness               string               `json:"freshness"`
	NativeCandidateLimit    int                  `json:"native_candidate_limit,omitempty"`
	PaginationStatus        string               `json:"pagination_status"`
	PageComplete            *bool                `json:"page_complete,omitempty"`
	NextCursor              string               `json:"next_cursor,omitempty"`
	NextArguments           *wakeHistoryArgs     `json:"next_arguments,omitempty"`
	CandidateTruncation     string               `json:"native_candidate_truncation"`
	CountBasis              string               `json:"count_basis"`
	NativeSelectedCount     int                  `json:"native_selected_instance_count"`
	NativeInstancesWithMeta int                  `json:"native_instances_with_meta"`
	AtCapacityCount         int                  `json:"at_capacity_count"`
	CapacityPresentCount    int                  `json:"at_capacity_present_count"`
	CapacityUnknownInMeta   int                  `json:"at_capacity_unknown_in_meta_count"`
	AtCapacityPct           *float64             `json:"at_capacity_pct,omitempty"`
	AtCapacityPctStatus     string               `json:"at_capacity_pct_status"`
	CapacityDenominator     string               `json:"at_capacity_denominator"`
	AdmissionCountBasis     string               `json:"admission_count_basis"`
	Wakes                   []wakeHistoryRowView `json:"wakes"`
	NativeCount             int                  `json:"native_returned_row_count"`
	ReturnedCount           int                  `json:"returned_row_count"`
	RowsTruncated           bool                 `json:"rows_truncated"`
	TextTruncated           bool                 `json:"text_truncated"`
	Order                   string               `json:"order"`
	Limitations             string               `json:"limitations"`
}

func (m *management) projectWakeHistory(slug, appID string, opts api.AppWakeTimelineOptions, limit int, n api.AppWakeTimelineResponse) (wakeHistoryView, error) {
	v := wakeHistoryView{App: slug, AppID: appID, Scope: "app_wide", Status: "observed", RequestedSince: opts.Since, RequestedUntil: opts.Until, WindowVerification: "not_echoed_by_native", WindowBounds: "inclusive_when_started_at_available", AsOf: n.AsOf,
		SourceStatus: "not_reported", SourceCompleteness: "unknown", Freshness: "unknown", NativeCandidateLimit: api.MCPWakeHistoryNativeCandidatesMax, CandidateTruncation: "not_reported", CountBasis: "latest_instance_candidates_filtered_by_requested_range",
		NativeSelectedCount: n.WakeCount24h, NativeInstancesWithMeta: n.WakeCountWithMeta, AtCapacityCount: n.AtCapacityCount, CapacityDenominator: "native_instances_with_meta", AtCapacityPctStatus: "no_meta_rows", AdmissionCountBasis: "native_admission_concurrency_ledger",
		Wakes: []wakeHistoryRowView{}, PaginationStatus: "latest_candidates_not_pageable", NativeCount: len(n.Rows), RowsTruncated: len(n.Rows) > limit, Order: "native_started_at_desc_to_second_precision", Limitations: wakeHistoryGuidance}
	known, err := m.validateWakeHistory(slug, appID, opts, n)
	if err != nil {
		return v, err
	}
	v.CapacityPresentCount, v.CapacityUnknownInMeta = known, n.WakeCountWithMeta-known
	if n.WakeCountWithMeta > 0 {
		v.AtCapacityPct, v.AtCapacityPctStatus = &n.AtCapacityPct, "observed_on_meta_rows"
		if v.CapacityUnknownInMeta > 0 {
			v.AtCapacityPctStatus = "native_denominator_includes_unknown_capacity"
		}
	}
	for _, row := range n.Rows[:min(len(n.Rows), limit)] {
		v.Wakes = append(v.Wakes, m.projectWakeHistoryRow(row, &v.TextTruncated))
	}
	v.ReturnedCount = len(v.Wakes)
	if len(n.Rows) == 0 {
		v.Status = "no_returned_wakes"
	}
	return v, nil
}

func (m *management) projectWakeHistoryRow(n api.WakeTimelineJSONRow, truncated *bool) wakeHistoryRowView {
	text := func(s string) string { return m.requestText(s, truncated) }
	v := wakeHistoryRowView{WakeID: n.WakeID, WakeIDStatus: "not_reported", Kind: n.Kind, State: text(n.State), At: n.At, StartedAtStatus: "not_reported", WindowMembership: "unverified_missing_started_at",
		Trigger: text(n.Trigger), TriggerClass: text(n.TriggerClass), Method: text(n.Method), Tier: text(n.Tier), QueuedCountStatus: "zero_or_not_reported", ConcurrencyStatus: "zero_or_not_reported", AtCapacityStatus: "not_reported", ReadinessStatus: "not_reported"}
	v.LabelsModified = v.State != n.State || v.Trigger != n.Trigger || v.TriggerClass != n.TriggerClass || v.Method != n.Method || v.Tier != n.Tier
	if n.WakeID != "" {
		v.WakeIDStatus = "observed"
	}
	if n.At != "" {
		v.StartedAtStatus, v.WindowMembership = "observed_to_second_precision", "compatible_at_native_second_precision"
	}
	if n.QueuedCount > 0 {
		v.QueuedCount, v.QueuedCountStatus = &n.QueuedCount, "observed"
	}
	if n.ConcurrencyAtAdmit > 0 {
		v.ConcurrencyAtAdmit, v.ConcurrencyStatus = &n.ConcurrencyAtAdmit, "observed"
	}
	if n.AtCapacityPresent {
		v.AtCapacity, v.AtCapacityStatus = &n.AtCapacity, "observed"
	}
	if n.ReadyInMS > 0 {
		v.ReadyInMS, v.ReadinessStatus = &n.ReadyInMS, "observed"
	}
	return v
}
