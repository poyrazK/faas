// Package profiling implements bounded CPU profile ingestion and queries.
// It runs outside vmmd's privileged boundary (ADR-819).
package profiling

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/google/pprof/profile"
	"github.com/onebox-faas/faas/pkg/api"
)

const CPUProfileType = "process_cpu:cpu:nanoseconds:cpu:nanoseconds"

// Parse bounds expansion before the protobuf parser allocates profile objects.
func Parse(data []byte) (*profile.Profile, error) {
	if len(data) == 0 || len(data) > api.ProfileMaxCompressedBytes {
		return nil, fmt.Errorf("profile size outside allowed bounds")
	}
	var reader io.Reader = bytes.NewReader(data)
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		gz, err := gzip.NewReader(reader)
		if err != nil {
			return nil, fmt.Errorf("profile compression: %w", err)
		}
		defer func() { _ = gz.Close() }()
		reader = gz
	}
	raw, err := io.ReadAll(io.LimitReader(reader, api.ProfileMaxExpandedBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read profile: %w", err)
	}
	if len(raw) > api.ProfileMaxExpandedBytes {
		return nil, fmt.Errorf("expanded profile exceeds limit")
	}
	p, err := profile.ParseUncompressed(raw)
	if err != nil {
		return nil, fmt.Errorf("parse profile: %w", err)
	}
	if err := p.CheckValid(); err != nil {
		return nil, fmt.Errorf("invalid profile: %w", err)
	}
	if len(p.Sample) > api.ProfileMaxNodes || len(p.Function) > api.ProfileMaxNodes || len(p.Location) > api.ProfileMaxNodes {
		return nil, fmt.Errorf("profile has too many nodes")
	}
	for _, f := range p.Function {
		if len(f.Name) > api.ProfileMaxSymbolBytes || len(f.Filename) > api.ProfileMaxSymbolBytes {
			return nil, fmt.Errorf("profile symbol exceeds bounds")
		}
	}
	for _, loc := range p.Location {
		if len(loc.Line) > api.ProfileMaxStackDepth {
			return nil, fmt.Errorf("profile inline stack is too deep")
		}
	}
	total := 0
	for _, sample := range p.Sample {
		depth := 0
		for _, loc := range sample.Location {
			if len(loc.Line) == 0 {
				depth++
			} else {
				depth += len(loc.Line)
			}
		}
		total += depth
		if depth > api.ProfileMaxStackDepth || total > api.ProfileMaxTotalFrames {
			return nil, fmt.Errorf("profile stack frames exceed bounds")
		}
	}

	return p, nil
}

// NormalizeCPU preserves only CPU nanoseconds. Wall-only profiles are rejected;
// their stacks must never be presented as measured CPU consumption.
func NormalizeCPU(p *profile.Profile) error {
	return normalizeCPU(p, false)
}

func normalizeCPU(p *profile.Profile, keepRoute bool) error {
	index, factor := -1, int64(0)
	for i, typ := range p.SampleType {
		if typ.Type == "cpu" && typ.Unit == "nanoseconds" {
			index, factor = i, 1
			break
		}
	}
	if index < 0 {
		return fmt.Errorf("profile contains no CPU time in nanoseconds")
	}
	for _, sample := range p.Sample {
		if sample.Value[index] < 0 {
			return fmt.Errorf("negative CPU sample")
		}
		sample.Value = []int64{sample.Value[index] * factor}
		// Customer sample labels can contain secrets, trace IDs and arbitrary
		// tenant selectors. Stack symbols remain; backend labels are host-owned.
		if !keepRoute {
			sample.Label = nil
		}
		sample.NumLabel = nil
		sample.NumUnit = nil
	}
	p.SampleType = []*profile.ValueType{{Type: "cpu", Unit: "nanoseconds"}}
	p.DefaultSampleType = "cpu"
	p.PeriodType = &profile.ValueType{Type: "cpu", Unit: "nanoseconds"}
	p.Comments = nil
	return nil
}

type frame struct {
	name, file string
	line       int64
}

func sampleFrames(s *profile.Sample) []frame {
	frames := make([]frame, 0, len(s.Location))
	for _, loc := range s.Location {
		if len(loc.Line) == 0 {
			frames = append(frames, frame{name: "[unknown]"})
			continue
		}
		for _, line := range loc.Line {
			f := frame{name: "[unknown]", line: line.Line}
			if line.Function != nil {
				f.name = line.Function.Name
				f.file = line.Function.Filename
			}
			frames = append(frames, f)
		}
	}
	return frames
}

func frameKey(f frame) string { return fmt.Sprintf("%s\x00%s\x00%d", f.name, f.file, f.line) }

func View(p *profile.Profile, q api.ProfileQuery) (api.ProfileResponse, error) {
	out := api.ProfileResponse{Query: q, Attribution: attributionQuality(0, 0), Routes: []api.ProfileRouteCPU{}, Functions: []api.ProfileFunction{}, Flamegraph: &api.ProfileStack{Name: "all", Children: []*api.ProfileStack{}}, Empty: true}
	if p == nil {
		return out, nil
	}
	if err := NormalizeCPU(p); err != nil {
		return out, err
	}
	functions := map[string]*api.ProfileFunction{}
	// Use parent identity plus frame key to retain distinct call paths.
	children := map[*api.ProfileStack]map[string]*api.ProfileStack{}
	nodes, symbolBytes := 0, 0
	costs := map[string]float64{}
	var wholeCPU, attributedCPU float64
	for _, s := range p.Sample {
		if s.Value[0] == 0 {
			continue
		}
		seconds := float64(s.Value[0]) / 1e9
		route, frames := sampleRoute(sampleFrames(s))
		wholeCPU += seconds
		if route != api.ProfileUnattributedRoute {
			attributedCPU += seconds
		}
		if q.Route != "" && q.Route != route {
			continue
		}
		if _, exists := costs[route]; !exists && len(costs) >= api.ProfileRouteMaxLabels+1 {
			return out, fmt.Errorf("route attribution exceeds query bounds")
		}
		costs[route] += seconds
		out.CPUSeconds += seconds
		out.StackCount++
		if len(frames) == 0 {
			frames = []frame{{name: "[unknown]"}}
		}
		seen := map[string]bool{}
		for i, f := range frames {
			key := frameKey(f)
			fn := functions[key]
			if fn == nil {
				symbolBytes += len(f.name) + len(f.file)
				if symbolBytes > api.ProfileMaxViewSymbolBytes {
					return out, fmt.Errorf("profile source symbols exceed query bounds")
				}
				fn = &api.ProfileFunction{Name: f.name, File: f.file, Line: f.line}
				functions[key] = fn
			}
			if !seen[key] {
				fn.TotalCPUSeconds += seconds
				seen[key] = true
			}
			if i == 0 {
				fn.SelfCPUSeconds += seconds
			}
		}
		node := out.Flamegraph
		node.CPUSeconds += seconds
		for i := len(frames) - 1; i >= 0; i-- {
			if children[node] == nil {
				children[node] = map[string]*api.ProfileStack{}
			}
			key := frameKey(frames[i])
			child := children[node][key]
			if child == nil {
				nodes++
				symbolBytes += len(frames[i].name) + len(frames[i].file)
				if nodes > api.ProfileMaxViewNodes || symbolBytes > api.ProfileMaxViewSymbolBytes {
					return out, fmt.Errorf("merged profile has too many call paths")
				}
				child = &api.ProfileStack{Name: frames[i].name, File: frames[i].file, Line: frames[i].line, Children: []*api.ProfileStack{}}
				children[node][key] = child
				node.Children = append(node.Children, child)
			}
			child.CPUSeconds += seconds
			node = child
		}
	}
	out.Attribution = attributionQuality(wholeCPU, attributedCPU)
	out.Routes = routeCPUView(costs)
	for _, f := range functions {
		out.Functions = append(out.Functions, *f)
	}
	sort.Slice(out.Functions, func(i, j int) bool {
		if out.Functions[i].SelfCPUSeconds != out.Functions[j].SelfCPUSeconds {
			return out.Functions[i].SelfCPUSeconds > out.Functions[j].SelfCPUSeconds
		}
		return functionKey(out.Functions[i]) < functionKey(out.Functions[j])
	})
	for parent := range children {
		sort.Slice(parent.Children, func(i, j int) bool { return parent.Children[i].Name < parent.Children[j].Name })
	}
	out.Empty = out.StackCount == 0
	return out, nil
}

func functionKey(f api.ProfileFunction) string { return frameKey(frame{f.Name, f.File, f.Line}) }

// Named functions can move or accumulate samples at different lines between
// deployments. Aggregate their self CPU by symbol and file for comparison.
// Keep unknown/anonymous frames separate because their line is their identity.
func comparisonKey(f api.ProfileFunction) string {
	if f.Name == "" || f.Name == "[unknown]" || f.Name == "(anonymous)" {
		return functionKey(f)
	}
	return f.Name + "\x00" + f.File
}

func Compare(a, b api.ProfileResponse) api.ProfileCompareResponse {
	out := api.ProfileCompareResponse{Baseline: a, Candidate: b, Attribution: CompareAttribution(a, b), Functions: []api.ProfileFunctionDelta{}}
	if reason := profileComparisonReason(a, b); reason != "" {
		out.Reason = reason
		return out
	}
	ad, bd := a.Query.End.Sub(a.Query.Start).Seconds(), b.Query.End.Sub(b.Query.Start).Seconds()
	rows := map[string]*api.ProfileFunctionDelta{}
	baselineLines, candidateLines := map[string]api.ProfileFunction{}, map[string]api.ProfileFunction{}
	for _, f := range a.Functions {
		key := comparisonKey(f)
		if rows[key] == nil {
			rows[key] = &api.ProfileFunctionDelta{Name: f.Name, File: f.File, Line: f.Line}
		}
		rows[key].BaselineCPUPerSecond += f.SelfCPUSeconds / ad
		rows[key].BaselineObserved = true
		if previous, ok := baselineLines[key]; !ok || preferredSource(f, previous) {
			baselineLines[key] = f
			rows[key].Line = f.Line
			rows[key].BaselineSource = f.Source
		}
	}
	for _, f := range b.Functions {
		key := comparisonKey(f)
		if rows[key] == nil {
			rows[key] = &api.ProfileFunctionDelta{Name: f.Name, File: f.File, Line: f.Line}
		}
		if previous, ok := candidateLines[key]; !ok || preferredSource(f, previous) {
			candidateLines[key] = f
			rows[key].Line = f.Line
			rows[key].CandidateSource = f.Source
		}
		rows[key].CandidateCPUPerSecond += f.SelfCPUSeconds / bd
		rows[key].CandidateObserved = true
	}
	for _, row := range rows {
		row.DeltaKnown = row.BaselineObserved && row.CandidateObserved
		if row.DeltaKnown {
			row.DeltaCPUPerSecond = row.CandidateCPUPerSecond - row.BaselineCPUPerSecond
		}
		out.Functions = append(out.Functions, *row)
	}
	sort.Slice(out.Functions, func(i, j int) bool {
		if out.Functions[i].DeltaCPUPerSecond != out.Functions[j].DeltaCPUPerSecond {
			return out.Functions[i].DeltaCPUPerSecond > out.Functions[j].DeltaCPUPerSecond
		}
		return strings.Compare(out.Functions[i].Name, out.Functions[j].Name) < 0
	})
	out.Comparable = true
	out.RouteAdjustment = routeAdjustment(a, b)
	var err error
	out.Flamegraph, err = differentialFlamegraph(a, b)
	if err != nil {
		out.FlamegraphReason = err.Error()
	}
	return out
}

func ValidateQuery(q api.ProfileQuery, now time.Time, retentionDays int) error {
	if !api.ValidProfileRoute(q.Route) {
		return fmt.Errorf("route must be a static method/pattern or [unattributed]")
	}
	if q.Start.IsZero() || !q.End.After(q.Start) || q.End.After(now.Add(time.Second)) || q.Start.Before(now.Add(-time.Duration(retentionDays)*24*time.Hour)) {
		return fmt.Errorf("profile query must be within retention and end after start")
	}
	return nil
}

func profileComparisonReason(a, b api.ProfileResponse) string {
	if a.Query.Route != b.Query.Route {
		return "Select the same route on both deployments."
	}
	if a.Empty || b.Empty {
		return "Both deployments need CPU samples before comparison."
	}
	if a.Query.Runtime == "" || a.Query.Runtime != b.Query.Runtime {
		return "Select the same runtime on both sides."
	}
	ad, bd := a.Query.End.Sub(a.Query.Start).Seconds(), b.Query.End.Sub(b.Query.Start).Seconds()
	if ad <= 0 || bd <= 0 {
		return "Select a positive capture window on both sides."
	}
	return ""
}
