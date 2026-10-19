package mcpmanagement

import (
	"math"

	"github.com/onebox-faas/faas/pkg/api"
)

type wakeTimelineDetailsView struct {
	Status                  string `json:"status"`
	Method                  string `json:"method,omitempty"`
	Tier                    string `json:"tier,omitempty"`
	Trigger                 string `json:"trigger,omitempty"`
	TriggerClass            string `json:"trigger_class,omitempty"`
	LabelsModified          bool   `json:"labels_modified"`
	RestoreFallbackReason   string `json:"restore_fallback_reason,omitempty"`
	RestoreFallbackStatus   string `json:"restore_fallback_status"`
	RequestedAt             string `json:"requested_at,omitempty"`
	ObservedAt              string `json:"observed_at,omitempty"`
	StartedAt               string `json:"started_at,omitempty"`
	CompletedAt             string `json:"completed_at,omitempty"`
	FailedAt                string `json:"failed_at,omitempty"`
	QueuedCount             *int64 `json:"queued_count,omitempty"`
	ConcurrencyAtAdmit      *int64 `json:"concurrency_at_admit,omitempty"`
	AtCapacity              *bool  `json:"at_capacity,omitempty"`
	AdmissionCountBasis     string `json:"admission_count_basis,omitempty"`
	ReadinessElapsedMS      *int64 `json:"readiness_elapsed_ms,omitempty"`
	ProbeCount              *int64 `json:"probe_count,omitempty"`
	AcceptanceToFirstByteMS *int64 `json:"acceptance_to_first_byte_ms,omitempty"`
	PostWakeProxyMS         *int64 `json:"post_wake_proxy_ms,omitempty"`
	NativeCallTotalMS       *int64 `json:"native_call_total_ms,omitempty"`
	WaitReadyMS             *int64 `json:"wait_ready_ms,omitempty"`
	NativeCallBasis         string `json:"native_call_basis,omitempty"`
}

func (m *management) projectWakeTimelineDetails(row api.WakeTimelineEvent, stage string, truncated *bool) (wakeTimelineDetailsView, error) {
	v := wakeTimelineDetailsView{Status: "selected_fields_only", RestoreFallbackStatus: "not_reported"}
	if stage == "unknown" {
		v.Status = "not_exposed_for_kind"
		return v, nil
	}
	p := wakeTimelinePayload{data: row.Data}
	label := func(key string) string {
		raw := p.text(key)
		out := m.requestText(raw, truncated)
		v.LabelsModified = v.LabelsModified || out != raw
		return out
	}
	switch row.Kind {
	case "wake.boot_started", "wake.boot_completed":
		v.Method, v.Tier, v.Trigger, v.TriggerClass = label("method"), label("tier"), label("trigger"), label("trigger_class")
		v.QueuedCount, v.ConcurrencyAtAdmit = p.integer("queued_count"), p.integer("concurrency_at_admit")
		v.AdmissionCountBasis = "native_admission_concurrency_ledger"
		if row.Kind == "wake.boot_started" {
			v.RequestedAt, v.AtCapacity = p.timestamp("requested_at"), p.boolean("at_capacity")
		} else {
			v.StartedAt, v.CompletedAt = p.timestamp("started_at"), p.timestamp("completed_at")
			if reason := p.text("restore_fallback_reason"); reason != "" {
				v.RestoreFallbackStatus = "unrecognized"
				if reason == "after_restore_failed" {
					v.RestoreFallbackReason, v.RestoreFallbackStatus = reason, "observed"
				}
			}
		}
	case "wake.boot_observed":
		v.Method, v.ObservedAt = label("method"), p.timestamp("observed_at")
	case "wake.boot_failed":
		v.Method, v.FailedAt = label("method"), p.timestamp("failed_at")
	case "wake.readiness_200":
		v.ReadinessElapsedMS, v.ProbeCount = p.integer("elapsed_ms"), p.integer("probe_count")
	case "wake.proxy_first_byte":
		v.AcceptanceToFirstByteMS, v.PostWakeProxyMS = p.integer("latency_ms"), p.integer("proxy_latency_ms")
	case "wake.restore_breakdown", "wake.cold_boot_breakdown":
		v.NativeCallTotalMS, v.WaitReadyMS = p.integer("total_ms"), p.integer("wait_ready_ms")
		v.NativeCallBasis = "restore_call"
		if row.Kind == "wake.cold_boot_breakdown" {
			v.NativeCallBasis = "cold_boot_call"
		}
	case "wake.park_started":
		v.StartedAt = p.timestamp("started_at")
	case "wake.park_completed":
		v.StartedAt, v.CompletedAt = p.timestamp("started_at"), p.timestamp("completed_at")
	case "wake.park_failed":
		v.StartedAt, v.FailedAt = p.timestamp("started_at"), p.timestamp("failed_at")
	}
	return v, p.err
}

// Read only explicitly selected payload fields. Arbitrary future/nested data is
// neither projected nor interpreted. Null and absent retain unknown presence.
type wakeTimelinePayload struct {
	data map[string]any
	err  error
}

func (p *wakeTimelinePayload) bad() {
	p.err = invalid("invalid_response", "native wake timeline has an invalid selected measurement or label")
}

func (p *wakeTimelinePayload) text(key string) string {
	raw, present := p.data[key]
	if !present || raw == nil {
		return ""
	}
	v, ok := raw.(string)
	if !ok {
		p.bad()
	}
	return v
}

func (p *wakeTimelinePayload) timestamp(key string) string {
	raw := p.text(key)
	if raw == "" {
		return ""
	}
	t, ok := wakeTimelineTime(raw)
	if !ok {
		p.bad()
		return ""
	}
	if t.IsZero() {
		return ""
	}
	return raw
}

func (p *wakeTimelinePayload) integer(key string) *int64 {
	raw, present := p.data[key]
	if !present || raw == nil {
		return nil
	}
	// The typed API client decodes map[string]any with encoding/json, so
	// integers arrive as float64. Accept only exact JSON-safe whole values.
	n, ok := raw.(float64)
	if !ok || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > float64(api.MCPWakeTimelineIntegerMax) || math.Trunc(n) != n {
		p.bad()
		return nil
	}
	v := int64(n)
	return &v
}

func (p *wakeTimelinePayload) boolean(key string) *bool {
	raw, present := p.data[key]
	if !present || raw == nil {
		return nil
	}
	v, ok := raw.(bool)
	if !ok {
		p.bad()
		return nil
	}
	return &v
}
