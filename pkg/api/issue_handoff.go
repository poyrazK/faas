package api

import "time"

// IssueHandoff is the opt-in issue.handoff webhook payload, captured atomically
// with its transition. Delivery retries never gather a different sample.
type IssueHandoff struct {
	SchemaVersion int                  `json:"schema_version"`
	ID            string               `json:"id"`
	Type          string               `json:"type"`
	GeneratedAt   time.Time            `json:"generated_at"`
	Issue         Issue                `json:"issue"`
	Transition    IssueActivity        `json:"transition"`
	Sample        *IssueEvent          `json:"sample,omitempty"`
	Release       *IssueRelease        `json:"release,omitempty"`
	Request       *IssueHandoffRequest `json:"request,omitempty"`
	Impact        IssueImpact          `json:"impact"`
	Evidence      IssueHandoffEvidence `json:"evidence"`
	Gaps          []string             `json:"gaps"`
}

// IssueHandoffRequest contains singleton telemetry, never aggregate estimates,
// arbitrary span attributes, SQL statements, request bodies, or headers.
type IssueHandoffRequest struct {
	ID             string             `json:"id"`
	TraceID        string             `json:"trace_id,omitempty"`
	Route          string             `json:"route"`
	Method         string             `json:"method"`
	Status         int                `json:"status"`
	LatencyMS      int64              `json:"latency_ms"`
	ColdBoot       bool               `json:"cold_boot"`
	ReceivedAt     time.Time          `json:"received_at"`
	Spans          []IssueHandoffSpan `json:"spans"`
	SpansTruncated bool               `json:"spans_truncated"`
}

type IssueHandoffSpan struct {
	SpanID        string `json:"span_id"`
	ParentSpanID  string `json:"parent_span_id,omitempty"`
	Name          string `json:"name"`
	Kind          string `json:"kind,omitempty"`
	Status        string `json:"status,omitempty"`
	DurationNanos uint64 `json:"duration_nanos"`
}

// Relative API paths require the receiver's own authenticated account access.
// A webhook signature or issue reporting token grants no read access.
type IssueHandoffEvidence struct {
	IssuePath   string `json:"issue_path"`
	RequestPath string `json:"request_path,omitempty"`
	TracePath   string `json:"trace_path,omitempty"`
}
