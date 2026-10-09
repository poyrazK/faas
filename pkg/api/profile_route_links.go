package api

import (
	"encoding/json"
	"net/url"
	"time"
)

// ProfileRouteComparisonURL preserves the exact route and capture windows.
func ProfileRouteComparisonURL(slug string, a, b ProfileQuery, check ProfileRouteRegression) string {
	q := url.Values{"route": {check.Route}, "deployment_id": {b.DeploymentID}, "runtime": {b.Runtime}, "start": {b.Start.Format(time.RFC3339Nano)}, "end": {b.End.Format(time.RFC3339Nano)}, "baseline_id": {a.DeploymentID}, "baseline_start": {a.Start.Format(time.RFC3339Nano)}, "baseline_end": {a.End.Format(time.RFC3339Nano)}}
	for _, e := range check.CodeEvidence {
		if e.Kind != "call_path" {
			continue
		}
		// Only matching metadata belongs in the URL, never source URLs or metrics.
		frames := make([]ProfileCallPathFrame, len(e.Frames))
		for i, f := range e.Frames {
			frames[i] = ProfileCallPathFrame{Name: f.Name, File: f.File, Line: f.Line}
		}
		body, err := json.Marshal(struct {
			Kind   string                 `json:"kind"`
			Frames []ProfileCallPathFrame `json:"frames"`
		}{"call_path", frames})
		if err == nil && len(body) <= ProfileInvestigationMaxPathBytes {
			q.Set("route_finding", string(body))
		}
		break
	}
	return "/dashboard/apps/" + url.PathEscape(slug) + "/profiles?" + q.Encode() + "#diff-flamegraph"
}
