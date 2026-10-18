package mcpmanagement

import (
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func linkRequestPath(e api.DebugRequestEvidenceResponse, v requestPathView, refs map[string]int) requestPathLinks {
	links := requestPathLinks{byID: map[string]int{}}
	if e.CriticalPath == nil || len(e.Spans) == 0 || len(e.Spans) > api.DebugEvidenceMaxSpans || len(refs) == 0 {
		return links
	}
	seen := map[string]bool{}
	for _, s := range e.Spans {
		if validateRequestID(s.SpanID) != nil || seen[s.SpanID] {
			return requestPathLinks{} // Ambiguous source IDs cannot support citations.
		}
		seen[s.SpanID] = true
		if i, ok := refs[s.SpanID]; ok {
			p := v.Segments[i]
			if len(s.StartTime) > api.MCPRequestFieldMaxBytes || len(s.EndTime) > api.MCPRequestFieldMaxBytes {
				continue
			}
			start, se := time.Parse(time.RFC3339Nano, s.StartTime)
			end, ee := time.Parse(time.RFC3339Nano, s.EndTime)
			// Match immutable identity and exposed metrics/classifications to the
			// original retained span before resolving any native finding to it.
			limited := false
			if se == nil && ee == nil && s.ParentSpanID == e.CriticalPath.Spans[i].ParentSpanID && uint64(p.DurationMS) == s.DurationNanos/1_000_000 && p.StartTime == start.UTC().Format(time.RFC3339Nano) && p.EndTime == end.UTC().Format(time.RFC3339Nano) && p.Kind == requestPathCategory(s.Kind, "kind", &limited) && p.Status == requestPathCategory(s.Status, "status", &limited) && p.DependencyType == requestPathCategory(s.DependencyType, "type", &limited) && p.DependencyKind == requestPathCategory(s.DependencyKind, "dependency", &limited) {
				links.byID[s.SpanID] = i
			}
		}
	}
	if !e.SpansTruncated {
		first := e.Spans[0]
		if _, ok := links.byID[first.SpanID]; ok {
			links.slowestRetainedID = first.SpanID
			for _, s := range e.Spans[1:] {
				if s.DurationNanos > first.DurationNanos {
					links.slowestRetainedID = ""
					break
				}
			}
		}
	}
	return links
}

func projectRequestPathFinding(e requestEvidenceView, f api.DebugEvidenceFinding, links requestPathLinks) (requestFindingView, bool) {
	v := requestFindingView{Code: f.Code, Confidence: "unknown", EvidenceRefs: []string{}}
	if len(f.EvidenceRefs) != api.MCPRequestExplanationRefsMax {
		return v, false
	}
	id := links.slowestRetainedID
	switch f.Code {
	case "slowest_span":
		if f.EvidenceRefs[0] != "span:0" || id == "" {
			return v, false
		}
	case "dependency_failure":
		var ok bool
		id, ok = strings.CutPrefix(f.EvidenceRefs[0], "span:")
		if !ok {
			return v, false
		}
	default:
		return v, false
	}
	i, ok := links.byID[id]
	if !ok || i < 0 || i >= len(e.CriticalPath.Segments) {
		return v, false
	}
	s := e.CriticalPath.Segments[i]
	v.EvidenceRefs = []string{s.Ref}
	v.Approximate = e.Request.Count != nil && *e.Request.Count > 1
	if f.Code == "dependency_failure" {
		if e.Request.Count == nil || *e.Request.Count <= 0 || e.Request.Status < 500 || e.Request.Status > 599 || s.Status != "error" || s.DependencyType == "unknown" || s.DependencyType == "unclassified" {
			return v, false
		}
		v.Title = "A dependency segment recorded an error"
		v.Detail = "The retained failed request includes an error on this dependency segment. Coincidence does not establish root cause."
	} else {
		v.Title = "A slow retained span is on the selected path"
		v.Detail = fmt.Sprintf("The native-selected longest-duration retained span measures %dms on this path. Span wall time does not establish an exclusive bottleneck or full request latency.", s.DurationMS)
	}
	switch f.Confidence {
	case "high", "medium", "low":
		v.Confidence = f.Confidence
	}
	return v, true
}
