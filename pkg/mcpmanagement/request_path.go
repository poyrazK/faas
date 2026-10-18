package mcpmanagement

import (
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type requestPathSegment struct {
	Ref            string `json:"ref"`
	ParentRef      string `json:"parent_ref,omitempty"`
	ParentStatus   string `json:"parent_status"`
	Kind           string `json:"kind"`
	DependencyType string `json:"dependency_type"`
	DependencyKind string `json:"dependency_kind"`
	Status         string `json:"status"`
	StartTime      string `json:"start_time"`
	EndTime        string `json:"end_time"`
	DurationMS     int64  `json:"duration_ms"`
	ExclusiveMS    int64  `json:"exclusive_ms"`
}

type requestPathView struct {
	Status               string               `json:"status"`
	Basis                string               `json:"basis"`
	DurationMS           *int64               `json:"duration_ms,omitempty"`
	NativeComplete       bool                 `json:"native_complete"`
	Complete             bool                 `json:"complete"`
	SourceSpansTruncated bool                 `json:"source_spans_truncated"`
	Segments             []requestPathSegment `json:"segments"`
	NativeSegmentCount   int                  `json:"native_returned_segment_count"`
	SegmentsTruncated    bool                 `json:"segments_truncated"`
	CategoriesLimited    bool                 `json:"categories_limited"`
	SlowestStatus        string               `json:"slowest_status"`
	SlowestSegmentRef    string               `json:"slowest_segment_ref,omitempty"`
	SlowestExclusiveMS   *int64               `json:"slowest_exclusive_ms,omitempty"`
	SourceCompleteness   string               `json:"source_completeness"`
	Limitations          string               `json:"limitations"`
}

// Private lookup state never enters the serialized view. Native IDs are used
// only to verify links within this response and resolve supported findings.
type requestPathLinks struct {
	byID              map[string]int
	slowestRetainedID string
}

func projectRequestPath(n *api.DebugRequestCriticalPath, sourceTruncated bool) (requestPathView, map[string]int) {
	v := requestPathView{Status: "unavailable", Basis: "native_retained_span_parent_chain", SourceSpansTruncated: sourceTruncated, Segments: []requestPathSegment{}, SlowestStatus: "not_reported", SourceCompleteness: "unknown",
		Limitations: "Native selected parent chain reconstructed from retained span timing. Path duration is not full request latency; exclusive durations account for direct-child overlap and are not an additive latency total. A request row can represent multiple requests; this path does not establish timing for every represented request. Native completeness describes reconstruction, not capture of all traffic. Source and projection truncation remain separate. Segment refs are local JSON Pointers, not native IDs or selectors. Names, dependency names, SQL, trace/span IDs, raw attributes and error messages are omitted. Unknown categories remain unknown. Empty, partial or invalid paths do not prove health or root cause."}
	if n == nil {
		return v, nil
	}
	v.NativeComplete, v.NativeSegmentCount = n.Complete, len(n.Spans)
	v.SegmentsTruncated = len(n.Spans) > api.MCPRequestPathSegmentsMax
	if len(n.Spans) == 0 && n.SpanCount == 0 && n.DurationMS == 0 && n.SlowestSpanID == "" && n.SlowestExclusiveMS == 0 {
		v.Status = "no_retained_path"
		return v, nil
	}
	if !validRequestPath(n) {
		v.Status = "invalid_native_path"
		return v, nil
	}
	v.DurationMS = &n.DurationMS
	refs := map[string]int{}
	for i, s := range n.Spans[:min(len(n.Spans), api.MCPRequestPathSegmentsMax)] {
		start, _ := time.Parse(time.RFC3339Nano, s.StartTime)
		end, _ := time.Parse(time.RFC3339Nano, s.EndTime)
		p := requestPathSegment{Ref: fmt.Sprintf("/critical_path/segments/%d", i), ParentStatus: "root", StartTime: start.UTC().Format(time.RFC3339Nano), EndTime: end.UTC().Format(time.RFC3339Nano), DurationMS: s.DurationMS, ExclusiveMS: s.ExclusiveMS}
		p.Kind = requestPathCategory(s.Kind, "kind", &v.CategoriesLimited)
		p.DependencyType = requestPathCategory(s.DependencyType, "type", &v.CategoriesLimited)
		p.DependencyKind = requestPathCategory(s.DependencyKind, "dependency", &v.CategoriesLimited)
		p.Status = requestPathCategory(s.Status, "status", &v.CategoriesLimited)
		if i > 0 {
			p.ParentRef, p.ParentStatus = v.Segments[i-1].Ref, "included"
		} else if s.ParentSpanID != "" {
			p.ParentStatus = "not_retained"
		}
		refs[s.SpanID] = i
		v.Segments = append(v.Segments, p)
	}
	v.Complete = n.Complete && !sourceTruncated && !v.SegmentsTruncated && n.Spans[0].ParentSpanID == ""
	v.Status = "observed"
	if !v.Complete || v.CategoriesLimited {
		v.Status = "partial"
	}
	if n.SlowestSpanID != "" {
		v.SlowestStatus = "omitted_by_projection"
		if i, ok := refs[n.SlowestSpanID]; ok {
			v.SlowestStatus, v.SlowestSegmentRef, v.SlowestExclusiveMS = "included", v.Segments[i].Ref, &n.SlowestExclusiveMS
		}
	}
	return v, refs
}

func validRequestPath(n *api.DebugRequestCriticalPath) bool {
	if len(n.Spans) == 0 || len(n.Spans) > api.DebugCriticalPathMaxSpans || n.SpanCount != len(n.Spans) || n.DurationMS < 0 || n.SlowestExclusiveMS < 0 {
		return false
	}
	seen := map[string]bool{}
	var first, last time.Time
	maxExclusive, slowestExclusive := int64(0), int64(-1)
	for i, s := range n.Spans {
		if validateRequestID(s.SpanID) != nil || seen[s.SpanID] || s.ParentSpanID != "" && validateRequestID(s.ParentSpanID) != nil || s.DurationMS < 0 || s.ExclusiveMS < 0 || s.ExclusiveMS > s.DurationMS || len(s.StartTime) > api.MCPRequestFieldMaxBytes || len(s.EndTime) > api.MCPRequestFieldMaxBytes {
			return false
		}
		seen[s.SpanID] = true
		start, se := time.Parse(time.RFC3339Nano, s.StartTime)
		end, ee := time.Parse(time.RFC3339Nano, s.EndTime)
		if se != nil || ee != nil || end.Before(start) || !start.Add(end.Sub(start)).Equal(end) || end.Sub(start).Milliseconds() != s.DurationMS || i > 0 && s.ParentSpanID != n.Spans[i-1].SpanID {
			return false
		}
		if i == 0 {
			first = start
		}
		last = end
		maxExclusive = max(maxExclusive, s.ExclusiveMS)
		if s.SpanID == n.SlowestSpanID {
			slowestExclusive = s.ExclusiveMS
		}
	}
	if seen[n.Spans[0].ParentSpanID] || n.Complete && n.Spans[0].ParentSpanID != "" || last.After(first) && !first.Add(last.Sub(first)).Equal(last) || max(int64(0), last.Sub(first).Milliseconds()) != n.DurationMS {
		return false
	}
	return n.SlowestExclusiveMS == maxExclusive && (n.SlowestSpanID == "" && maxExclusive == 0 || n.SlowestSpanID != "" && slowestExclusive == maxExclusive)
}

func requestPathCategory(raw, field string, limited *bool) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	if field == "kind" {
		value = strings.TrimPrefix(value, "span_kind_")
	}
	switch field + ":" + value {
	case "kind:internal", "kind:server", "kind:client", "kind:producer", "kind:consumer",
		"type:managed_binding", "type:outbound_integration", "type:guest_transport", "type:platform_internal", "type:app_dependency",
		"dependency:postgres", "dependency:postgresql", "dependency:mysql", "dependency:sqlite", "dependency:redis", "dependency:mongodb", "dependency:http", "dependency:https", "dependency:grpc", "dependency:kafka", "dependency:nats", "dependency:rabbitmq", "dependency:queue", "dependency:cache", "dependency:object_storage",
		"status:ok", "status:error":
		return value
	}
	if value == "" && (field == "type" || field == "dependency") {
		return "unclassified"
	}
	*limited = true
	return "unknown"
}

const requestPathGuidance = "Inspect critical_path as the native selected span-chain projection for this request. Its duration is reconstructed wall time, not full request latency, and exclusive durations must not be summed. Report native_complete separately from complete, source/projection truncation, unknown categories and source_completeness. Local refs and parent_ref are JSON Pointers in this response, never native span IDs or tool selectors. An omitted slowest segment must not be replaced with a smaller visible segment. Missing or invalid paths do not establish health. One path cannot establish timings for all requests represented by an aggregate row. Use span findings only with their verified included segment references; shared evidence and native confidence do not prove root cause. Do not request raw traces, names, SQL, attributes or native span IDs, or execute investigation suggestions automatically."
