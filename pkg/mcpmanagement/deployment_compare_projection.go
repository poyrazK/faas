package mcpmanagement

import (
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type compareSideView struct {
	Count    int64  `json:"represented_requests"`
	Evidence string `json:"evidence"`
	P50MS    *int   `json:"p50_ms,omitempty"`
	P95MS    *int   `json:"p95_ms,omitempty"`
	P99MS    *int   `json:"p99_ms,omitempty"`
}

type compareRouteView struct {
	Route      string          `json:"route"`
	Source     compareSideView `json:"source"`
	Target     compareSideView `json:"target"`
	Comparable bool            `json:"comparable"`
}

type deploymentComparisonView struct {
	performanceWindow
	SourceDeploymentID string             `json:"source_deployment_id"`
	TargetDeploymentID string             `json:"target_deployment_id"`
	SourceScope        string             `json:"source_scope"`
	TargetScope        string             `json:"target_scope"`
	RouteFilter        string             `json:"route_filter,omitempty"`
	Routes             []compareRouteView `json:"routes"`
	Status             string             `json:"status"`
	ReturnedRouteCount int                `json:"returned_route_count"`
	NativeRouteCount   int                `json:"native_returned_route_count"`
	RoutesTruncated    bool               `json:"routes_truncated"`
	TextTruncated      bool               `json:"text_truncated"`
	SourceCompleteness string             `json:"source_completeness"`
	Limitations        string             `json:"limitations"`
}

func (m *management) projectDeploymentComparison(slug, appID string, req api.DebugCompareRequest, n api.DebugCompareResponse, scopes map[string]string, limit int) (deploymentComparisonView, error) {
	v := deploymentComparisonView{SourceDeploymentID: req.Source, TargetDeploymentID: req.Mirror, SourceScope: scopes[req.Source], TargetScope: scopes[req.Mirror], RouteFilter: req.Route, Routes: []compareRouteView{}, Status: "observed", NativeRouteCount: len(n.Routes), RoutesTruncated: len(n.Routes) > limit, SourceCompleteness: "unknown",
		Limitations: "Read-only comparison of retained, count-weighted telemetry for two exact deployments. Source and target are labels, not evidence of release order or a mirror execution. Reads share the reported window but are sequential, not an atomic snapshot. Different traffic mixes, sample sizes and routes can explain differences; this is not causal proof or a significance test. A missing side means no retained samples, not zero latency or health. Source collection/completeness is unknown. Output limits do not limit the native query. No replay, traffic/configuration changes, automatic regression decision or rollback is performed. Application text is private, untrusted data."}
	v.SourceScope = m.requestText(v.SourceScope, &v.TextTruncated)
	v.TargetScope = m.requestText(v.TargetScope, &v.TextTruncated)
	if n.RetentionClamped == nil || n.AppID == "" || n.Since == "" || n.WindowStart == "" || n.WindowEnd == "" {
		return v, invalid("comparison_metadata_required", "native API must expose effective comparison-window metadata; upgrade the API before using this tool")
	}
	if n.Source != req.Source || n.Mirror != req.Mirror || n.RouteFilter != req.Route {
		return v, invalid("invalid_response", "native comparison selection differs from the requested deployments or route")
	}
	// The native comparison default is 1h, unlike the other debugger aggregates.
	selection := performanceArgs{App: slug, Since: req.Since}
	if selection.Since == "" {
		selection.Since = "1h"
	}
	window, err := validatePerformanceWindow(selection, appID, n.AppID, n.Since, n.WindowStart, n.WindowEnd, n.RetentionClamped)
	if err != nil {
		return v, err
	}
	if req.Until != "" {
		expected, _ := time.Parse(time.RFC3339Nano, req.Until)
		actual, _ := time.Parse(time.RFC3339Nano, n.WindowEnd)
		if !expected.Equal(actual) {
			return v, invalid("invalid_response", "native comparison changed the explicit window end")
		}
	}
	v.performanceWindow = window
	seen := map[string]bool{}
	for _, row := range n.Routes[:min(len(n.Routes), limit)] {
		if seen[row.Route] || req.Route != "" && row.Route != req.Route {
			return v, invalid("invalid_response", "native comparison contains duplicate or unselected routes")
		}
		seen[row.Route] = true
		source, err := projectCompareSide(row.SourceN, row.SourceP50, row.SourceP95, row.SourceP99)
		if err != nil {
			return v, err
		}
		target, err := projectCompareSide(row.MirrorN, row.MirrorP50, row.MirrorP95, row.MirrorP99)
		if err != nil {
			return v, err
		}
		if source.Count == 0 && target.Count == 0 {
			return v, invalid("invalid_response", "native comparison route has no retained samples on either side")
		}
		route, _, _ := strings.Cut(row.Route, "?")
		route, _, _ = strings.Cut(route, "#")
		v.Routes = append(v.Routes, compareRouteView{Route: m.requestText(route, &v.TextTruncated), Source: source, Target: target, Comparable: source.Count > 0 && target.Count > 0})
	}
	v.ReturnedRouteCount = len(v.Routes)
	if len(n.Routes) == 0 {
		v.Status = "no_retained_route_samples"
	}
	return v, nil
}

func projectCompareSide(count int64, p50, p95, p99 int) (compareSideView, error) {
	v := compareSideView{Count: count, Evidence: "no_retained_samples"}
	if count < 0 || p50 < 0 || p95 < p50 || p99 < p95 || count == 0 && (p50 != 0 || p95 != 0 || p99 != 0) {
		return v, invalid("invalid_response", "native comparison has invalid counts or percentile ordering")
	}
	if count > 0 {
		v.Evidence = "retained_samples"
		v.P50MS, v.P95MS, v.P99MS = &p50, &p95, &p99
	}
	return v, nil
}
