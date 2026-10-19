package mcpmanagement

import (
	"math"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type profileFunctionView struct {
	Name            string  `json:"name"`
	LabelsModified  bool    `json:"labels_modified"`
	SelfCPUSeconds  float64 `json:"self_cpu_seconds"`
	TotalCPUSeconds float64 `json:"total_cpu_seconds"`
}
type profilesView struct {
	App                string                 `json:"app"`
	AppID              string                 `json:"app_id"`
	Scope              string                 `json:"scope"`
	DeploymentID       string                 `json:"deployment_id"`
	Runtime            string                 `json:"runtime"`
	Start              string                 `json:"start"`
	End                string                 `json:"end"`
	Route              string                 `json:"route,omitempty"`
	WindowVerification string                 `json:"window_verification"`
	Status             string                 `json:"status"`
	CPUSeconds         *float64               `json:"sampled_cpu_seconds,omitempty"`
	SampleEntries      int64                  `json:"native_nonzero_sample_entries"`
	CountBasis         string                 `json:"count_basis"`
	NativeCount        int                    `json:"native_function_count"`
	ReturnedCount      int                    `json:"returned_function_count"`
	Limit              int                    `json:"limit"`
	FunctionsTruncated bool                   `json:"functions_truncated"`
	TextTruncated      bool                   `json:"text_truncated"`
	Functions          []profileFunctionView  `json:"functions"`
	Coverage           profileCoverageView    `json:"coverage"`
	Attribution        profileAttributionView `json:"attribution"`
	SourceCompleteness string                 `json:"source_completeness"`
	Freshness          string                 `json:"freshness"`
	Limitations        string                 `json:"limitations"`
}

func profileNumber(n float64) bool { return n >= 0 && !math.IsNaN(n) && !math.IsInf(n, 0) }
func profileClose(a, b float64) bool {
	return profileNumber(a) && profileNumber(b) && math.Abs(a-b) <= math.Max(1e-9, math.Max(a, b)*1e-6)
}

func (m *management) projectProfiles(slug, appID string, q api.ProfileQuery, limit int, n api.ProfileResponse) (profilesView, error) {
	v := profilesView{App: slug, AppID: appID, Scope: "app_wide", DeploymentID: q.DeploymentID, Runtime: q.Runtime, Start: q.Start.Format(time.RFC3339Nano), End: q.End.Format(time.RFC3339Nano), Route: q.Route, WindowVerification: "native_query_echo", Status: "observed", SampleEntries: n.StackCount, CountBasis: "native_nonzero_sample_entries", NativeCount: len(n.Functions), Limit: limit, FunctionsTruncated: len(n.Functions) > limit, Functions: []profileFunctionView{}, SourceCompleteness: "unknown", Freshness: "unknown", Limitations: profilesGuidance}
	fail := invalid("invalid_response", "native profiles have inconsistent selection, sample counts or function CPU measurements")
	if n.Query.DeploymentID != q.DeploymentID || n.Query.Runtime != q.Runtime || n.Query.Route != q.Route || !n.Query.Start.Equal(q.Start) || !n.Query.End.Equal(q.End) || !profileNumber(n.CPUSeconds) || n.StackCount < 0 || n.StackCount > api.ProfileMaxNodes || n.Empty != (n.StackCount == 0) || n.Empty != (n.CPUSeconds == 0) || n.Empty && len(n.Functions) != 0 || !n.Empty && len(n.Functions) == 0 || len(n.Functions) > api.ProfileMaxViewNodes || limit < 1 || limit > api.MCPProfileFunctionsMax {
		return v, fail
	}
	if n.Empty {
		v.Status = "no_selected_samples"
	} else {
		v.CPUSeconds = &n.CPUSeconds
	}
	previous := math.Inf(1)
	selfCPU := 0.0
	for i, f := range n.Functions {
		if !profileNumber(f.SelfCPUSeconds) || !profileNumber(f.TotalCPUSeconds) || f.SelfCPUSeconds > f.TotalCPUSeconds && !profileClose(f.SelfCPUSeconds, f.TotalCPUSeconds) || f.TotalCPUSeconds > n.CPUSeconds && !profileClose(f.TotalCPUSeconds, n.CPUSeconds) || f.SelfCPUSeconds > previous {
			return v, fail
		}
		previous = f.SelfCPUSeconds
		selfCPU += f.SelfCPUSeconds
		if i >= limit {
			continue
		}
		name := m.requestText(f.Name, &v.TextTruncated)
		if name == "" {
			name = "[unnamed]"
		}
		v.Functions = append(v.Functions, profileFunctionView{Name: name, LabelsModified: name != f.Name, SelfCPUSeconds: f.SelfCPUSeconds, TotalCPUSeconds: f.TotalCPUSeconds})
	}
	if !profileClose(selfCPU, n.CPUSeconds) {
		return v, fail
	}
	var err error
	v.Coverage, err = projectProfileCoverage(q, n.Coverage)
	if err != nil {
		return v, err
	}
	v.Attribution, err = projectProfileAttribution(q, n)
	if err != nil {
		return v, err
	}
	v.ReturnedCount = len(v.Functions)
	return v, nil
}
