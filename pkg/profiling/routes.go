package profiling

import (
	"sort"
	"strings"

	"github.com/google/pprof/profile"
	"github.com/onebox-faas/faas/pkg/api"
)

const routeFramePrefix = "[gregale-route] "
const routeFrameFile = "[gregale:route:v1]"

// Root markers survive backend stack merging, which can discard pprof labels.
// Remove guest-supplied markers before applying the host-validated label.
func sanitizeRouteSamples(p *profile.Profile, allowed []string) []string {
	reasons := make([]string, len(p.Sample))
	labels := map[string]bool{}
	for _, r := range allowed {
		if r != "" && r != api.ProfileUnattributedRoute && api.ValidProfileRoute(r) {
			labels[r] = true
		}
	}
	for i, s := range p.Sample {
		route := ""
		reasons[i] = "unlabeled"
		if values := s.Label[api.ProfileRouteLabel]; len(values) == 1 && labels[values[0]] {
			route = values[0]
			reasons[i] = "attributed"
		} else if len(values) > 0 && !(len(values) == 1 && values[0] == "") {
			reasons[i] = "invalid_label"
			if len(values) == 1 && values[0] != "" && api.ValidProfileRoute(values[0]) {
				reasons[i] = "route_not_admitted"
			}
		}
		for _, loc := range s.Location {
			for _, line := range loc.Line {
				if line.Function != nil && strings.HasPrefix(line.Function.Name, routeFramePrefix) {
					line.Function.Name = "[unknown]"
					line.Function.Filename = ""
				}
			}
		}
		s.Label = nil
		if route != "" {
			s.Label = map[string][]string{api.ProfileRouteLabel: {route}}
		}
	}
	return reasons
}

func encodeRouteFrames(p *profile.Profile, diagnostics ...[]string) {
	var reasons []string
	if len(diagnostics) == 1 && len(diagnostics[0]) == len(p.Sample) {
		reasons = diagnostics[0]
	}
	var functionID, locationID uint64
	for _, f := range p.Function {
		if f.ID > functionID {
			functionID = f.ID
		}
	}
	for _, l := range p.Location {
		if l.ID > locationID {
			locationID = l.ID
		}
	}
	markers := map[string]*profile.Location{}
	for i, s := range p.Sample {
		values := s.Label[api.ProfileRouteLabel]
		s.Label = nil
		if len(values) != 1 {
			continue
		}
		if reasons != nil {
			reasons[i] = "encoding_limit"
		}
		if len(sampleFrames(s)) >= api.ProfileMaxStackDepth {
			continue
		}
		route := values[0]
		loc := markers[route]
		if loc == nil {
			if functionID == ^uint64(0) || locationID == ^uint64(0) || len(p.Function) >= api.ProfileMaxNodes || len(p.Location) >= api.ProfileMaxNodes {
				continue
			}
			functionID++
			locationID++
			f := &profile.Function{ID: functionID, Name: routeFramePrefix + route, Filename: routeFrameFile}
			loc = &profile.Location{ID: locationID, Line: []profile.Line{{Function: f}}}
			p.Function = append(p.Function, f)
			p.Location = append(p.Location, loc)
			markers[route] = loc
		}
		s.Location = append(s.Location, loc)
		if reasons != nil {
			reasons[i] = "attributed"
		}
	}
}

func sampleRoute(frames []frame) (string, []frame) {
	// Backend merging can relocate or repeat synthetic frames. Remove every
	// reserved marker, and reject conflicting route claims.
	route := api.ProfileUnattributedRoute
	clean := make([]frame, 0, len(frames))
	conflict := false
	for _, marker := range frames {
		if marker.file == routeFrameFile && strings.HasPrefix(marker.name, routeFramePrefix) {
			value := strings.TrimPrefix(marker.name, routeFramePrefix)
			if value != "" && value != api.ProfileUnattributedRoute && api.ValidProfileRoute(value) {
				if route != api.ProfileUnattributedRoute && route != value {
					conflict = true
				}
				route = value
			}
			continue
		}
		clean = append(clean, marker)
	}
	if conflict {
		route = api.ProfileUnattributedRoute
	}
	return route, clean
}

func routeCPUView(costs map[string]float64) []api.ProfileRouteCPU {
	rows := make([]api.ProfileRouteCPU, 0, len(costs))
	for route, cpu := range costs {
		rows = append(rows, api.ProfileRouteCPU{Route: route, CPUSeconds: cpu})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].CPUSeconds != rows[j].CPUSeconds {
			return rows[i].CPUSeconds > rows[j].CPUSeconds
		}
		return rows[i].Route < rows[j].Route
	})
	return rows
}
