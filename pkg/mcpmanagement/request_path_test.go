// adr: 1626
package mcpmanagement

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const pathRootID = "2222222222222222"
const pathChildID = "1111111111111111"
const pathLeafID = "0000000000000001"

func withRequestPath(e api.DebugRequestEvidenceResponse) api.DebugRequestEvidenceResponse {
	e.CriticalPath = &api.DebugRequestCriticalPath{DurationMS: 90, Complete: true, SpanCount: 3, SlowestSpanID: pathLeafID, SlowestSpanName: "excluded-slowest-name", SlowestExclusiveMS: 50,
		Spans: []api.DebugCriticalPathSpan{
			{SpanID: pathRootID, Name: "excluded-root-name " + testToken, Kind: "server", Status: "ok", StartTime: "2026-10-17T10:00:00Z", EndTime: "2026-10-17T10:00:00.090Z", DurationMS: 90, ExclusiveMS: 10},
			{SpanID: pathChildID, ParentSpanID: pathRootID, Name: "excluded-sql SELECT private", Kind: "client", Status: "ok", DependencyType: "app_dependency", DependencyKind: "postgresql", DependencyName: "excluded-database-name", StartTime: "2026-10-17T10:00:00.010Z", EndTime: "2026-10-17T10:00:00.090Z", DurationMS: 80, ExclusiveMS: 30},
			{SpanID: pathLeafID, ParentSpanID: pathChildID, Name: "excluded-private-url", Kind: "client", Status: "error", DependencyType: "app_dependency", DependencyKind: "http", DependencyName: "excluded-private-host", StartTime: "2026-10-17T10:00:00.040Z", EndTime: "2026-10-17T10:00:00.090Z", DurationMS: 50, ExclusiveMS: 50},
		}}
	e.Spans = []api.DebugTelemetrySpan{}
	for _, p := range e.CriticalPath.Spans {
		e.Spans = append(e.Spans, api.DebugTelemetrySpan{TraceID: "excluded-trace", SpanID: p.SpanID, ParentSpanID: p.ParentSpanID, Name: p.Name, Kind: p.Kind, Status: p.Status, DependencyType: p.DependencyType, DependencyKind: p.DependencyKind, DependencyName: p.DependencyName, StartTime: p.StartTime, EndTime: p.EndTime, DurationNanos: uint64(p.DurationMS) * 1_000_000})
	}
	e.Spans[1].DBStatement = "excluded-sql"
	e.Explanation.Findings = append(e.Explanation.Findings,
		api.DebugEvidenceFinding{Code: "slowest_span", Title: "excluded-native-title", Detail: "excluded-prose", Confidence: "medium", EvidenceRefs: []string{"span:0"}},
		api.DebugEvidenceFinding{Code: "dependency_failure", Detail: "excluded-prose", Confidence: "high", EvidenceRefs: []string{"span:" + pathLeafID}})
	e.Explanation.Recommendations = append(e.Explanation.Recommendations, api.DebugEvidenceRecommendation{Action: "inspect_dependency_failure", Detail: "excluded-prose"})
	return e
}

func TestRequestPathProjectionAndFindingReferences(t *testing.T) {
	e := withRequestPath(requestFixtureEvidence())
	m := &management{}
	v := m.projectRequestEvidence(explanationFixtureView().Request, e, nil)
	p := v.CriticalPath
	if p.Status != "observed" || !p.NativeComplete || !p.Complete || p.DurationMS == nil || *p.DurationMS != 90 || len(p.Segments) != 3 || p.SlowestSegmentRef != "/critical_path/segments/2" || p.SlowestExclusiveMS == nil || *p.SlowestExclusiveMS != 50 || p.Segments[1].ParentRef != "/critical_path/segments/0" || p.Segments[2].DependencyKind != "http" {
		t.Fatalf("path metadata or references lost: %+v", p)
	}
	if len(v.Explanation.Findings) != 4 || v.Explanation.Findings[2].Code != "slowest_span" || v.Explanation.Findings[2].EvidenceRefs[0] != "/critical_path/segments/0" || v.Explanation.Findings[3].Code != "dependency_failure" || v.Explanation.Findings[3].EvidenceRefs[0] != p.SlowestSegmentRef || len(v.Explanation.Recommendations) != 2 {
		t.Fatalf("verified path findings lost: %+v", v.Explanation)
	}
	encoded, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"excluded-", "SELECT", pathRootID, pathChildID, pathLeafID, "trace_id", "span_id", "parent_span_id", "db_statement", "dependency_name"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Errorf("private path data leaked: %s", forbidden)
		}
	}
	assertExplanationReferences(t, v)
}

func TestRequestPathAvailabilityAndValidation(t *testing.T) {
	for _, mode := range []string{"absent", "empty", "zero_duration", "partial", "source_truncated", "missing_parent", "categories", "bad_count", "bad_id", "duplicate_id", "cycle", "broken_link", "claimed_complete", "bad_time", "reversed_time", "wrong_duration", "negative_exclusive", "excess_exclusive", "wrong_path_duration", "wrong_slowest", "missing_slowest", "wrong_slowest_metric", "long_timestamp", "native_bound"} {
		t.Run(mode, func(t *testing.T) {
			e := withRequestPath(requestFixtureEvidence())
			n := e.CriticalPath
			want := "invalid_native_path"
			switch mode {
			case "absent":
				n, want = nil, "unavailable"
			case "empty":
				n, want = &api.DebugRequestCriticalPath{}, "no_retained_path"
			case "zero_duration":
				n.DurationMS, n.SlowestExclusiveMS, n.SlowestSpanID = 0, 0, ""
				for i := range n.Spans {
					n.Spans[i].StartTime, n.Spans[i].EndTime = "2026-10-17T10:00:00Z", "2026-10-17T10:00:00Z"
					n.Spans[i].DurationMS, n.Spans[i].ExclusiveMS = 0, 0
				}
				want = "observed"
			case "partial":
				n.Complete, want = false, "partial"
			case "source_truncated":
				e.SpansTruncated, want = true, "partial"
			case "missing_parent":
				n.Complete, n.Spans[0].ParentSpanID, want = false, "excluded-missing-parent", "partial"
			case "categories":
				n.Spans[0].Kind, n.Spans[1].DependencyKind, n.Spans[2].Status = testToken, "excluded-query", "excluded-status"
				want = "partial"
			case "bad_count":
				n.SpanCount++
			case "bad_id":
				n.Spans[0].SpanID = ""
			case "duplicate_id":
				n.Spans[1].SpanID = pathRootID
			case "cycle":
				n.Complete, n.Spans[0].ParentSpanID = false, pathLeafID
			case "broken_link":
				n.Spans[2].ParentSpanID = pathRootID
			case "claimed_complete":
				n.Spans[0].ParentSpanID = "excluded-missing-parent"
			case "bad_time":
				n.Spans[0].StartTime = "invalid"
			case "reversed_time":
				n.Spans[1].StartTime = "2026-10-17T10:00:01Z"
			case "wrong_duration":
				n.Spans[0].DurationMS++
			case "negative_exclusive":
				n.Spans[0].ExclusiveMS = -1
			case "excess_exclusive":
				n.Spans[0].ExclusiveMS = 100
			case "wrong_path_duration":
				n.DurationMS++
			case "wrong_slowest":
				n.SlowestSpanID = "excluded-unknown-id"
			case "missing_slowest":
				n.SlowestSpanID = ""
			case "wrong_slowest_metric":
				n.SlowestExclusiveMS++
			case "long_timestamp":
				n.Spans[0].StartTime = "2026-10-17T10:00:00." + strings.Repeat("0", api.MCPRequestFieldMaxBytes) + "Z"
			case "native_bound":
				n.Spans = make([]api.DebugCriticalPathSpan, api.DebugCriticalPathMaxSpans+1)
				n.SpanCount = len(n.Spans)
			}
			v, refs := projectRequestPath(n, e.SpansTruncated)
			if v.Status != want || v.SourceCompleteness != "unknown" || v.Segments == nil {
				t.Fatalf("path status=%s want=%s", v.Status, want)
			}
			if want == "invalid_native_path" && (len(v.Segments) != 0 || v.DurationMS != nil || len(refs) != 0 || v.Complete) {
				t.Fatal("invalid path released timing, segments or completeness")
			}
			if mode == "missing_parent" && (v.Segments[0].ParentStatus != "not_retained" || v.Segments[0].ParentRef != "" || v.Complete) {
				t.Fatal("invented a missing parent reference")
			}
			if mode == "source_truncated" && (v.Complete || !v.SourceSpansTruncated) {
				t.Fatal("source truncation lost")
			}
			if mode == "zero_duration" && (v.DurationMS == nil || *v.DurationMS != 0 || len(v.Segments) != 3 || v.SlowestStatus != "not_reported") {
				t.Fatal("observed zero durations became missing evidence")
			}
			if mode == "categories" && (!v.CategoriesLimited || v.Segments[0].Kind != "unknown" || v.Segments[1].DependencyKind != "unknown") {
				t.Fatal("unreviewed labels exported")
			}
		})
	}
}

func TestRequestPathProjectionBound(t *testing.T) {
	n := &api.DebugRequestCriticalPath{Complete: true, DurationMS: 10, SpanCount: api.MCPRequestPathSegmentsMax + 1, SlowestExclusiveMS: 10}
	for i := 0; i < n.SpanCount; i++ {
		p := api.DebugCriticalPathSpan{SpanID: fmt.Sprintf("private-%d", i), Kind: "internal", Status: "ok", StartTime: "2026-10-17T10:00:00Z", EndTime: "2026-10-17T10:00:00.010Z", DurationMS: 10}
		if i > 0 {
			p.ParentSpanID = n.Spans[i-1].SpanID
		}
		if i == n.SpanCount-1 {
			p.ExclusiveMS, n.SlowestSpanID = 10, p.SpanID
		}
		n.Spans = append(n.Spans, p)
	}
	v, refs := projectRequestPath(n, false)
	if v.Status != "partial" || v.Complete || !v.SegmentsTruncated || len(v.Segments) != api.MCPRequestPathSegmentsMax || len(refs) != api.MCPRequestPathSegmentsMax || v.SlowestStatus != "omitted_by_projection" || v.SlowestSegmentRef != "" || v.SlowestExclusiveMS != nil {
		t.Fatal("path limit lost or unprojected slowest reference exposed")
	}
}

func TestRequestPathFindingsRequireMatchingRetainedSpans(t *testing.T) {
	for _, mode := range []string{"source_duplicate", "unmatched_duration", "unmatched_parent", "unmatched_time", "unmatched_status", "source_truncated", "unsorted_source", "missing_raw", "unknown_kind", "request_success", "not_selected"} {
		t.Run(mode, func(t *testing.T) {
			e := withRequestPath(requestFixtureEvidence())
			switch mode {
			case "source_duplicate":
				e.Spans = append(e.Spans, e.Spans[0])
			case "unmatched_duration":
				e.Spans[2].DurationNanos++
				e.Spans[2].DurationNanos += uint64(time.Millisecond)
			case "unmatched_parent":
				e.Spans[2].ParentSpanID = "excluded-parent"
			case "unmatched_time":
				e.Spans[2].EndTime = "2026-10-17T10:00:01Z"
			case "unmatched_status":
				e.Spans[2].Status = "ok"
			case "source_truncated":
				e.SpansTruncated = true
			case "unsorted_source":
				e.Spans[1].DurationNanos = uint64(time.Second)
			case "missing_raw":
				e.Spans = nil
			case "unknown_kind":
				e.CriticalPath.Spans[2].DependencyType, e.Spans[2].DependencyType = "excluded-kind", "excluded-kind"
			case "request_success":
				e.Request.Status = 200
			case "not_selected":
				e.Explanation.Findings[5].EvidenceRefs = []string{"span:excluded-unselected"}
			}
			m := &management{}
			v := m.projectRequestEvidence(m.projectRequest(requestArgs{App: "alpha", RequestID: "row-1"}, "app-1", "default", e.Request), e, nil)
			for _, f := range v.Explanation.Findings {
				if (mode == "source_truncated" || mode == "unsorted_source") && f.Code == "slowest_span" {
					t.Fatal("unverified longest span finding released")
				}
				if mode != "source_truncated" && mode != "unsorted_source" && f.Code == "dependency_failure" {
					t.Fatal("unmatched dependency span finding released")
				}
			}
			assertExplanationReferences(t, v)
		})
	}
}
