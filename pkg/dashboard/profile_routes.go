package dashboard

import (
	"net/url"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type ProfileRouteView struct {
	Attribution          *api.ProfileAttributionComparison
	CandidateAttribution *api.ProfileAttributionQuality
	Selected             string
	Rows                 []ProfileRouteRow
	Adjustment           *api.ProfileRouteAdjustment
}
type ProfileRouteRow struct {
	Route, URL                string
	Baseline, Candidate       *api.ProfileRouteCPU
	DeltaCPUSecondsPerRequest *float64
}

func BuildProfileRoutes(slug string, q, b api.ProfileQuery, p *api.ProfileResponse, comparison *api.ProfileCompareResponse) *ProfileRouteView {
	v := &ProfileRouteView{Selected: q.Route, Rows: []ProfileRouteRow{}}
	rows := map[string]*ProfileRouteRow{}
	add := func(profile *api.ProfileResponse, baseline bool) {
		if profile == nil {
			return
		}
		for _, cost := range profile.Routes {
			row := rows[cost.Route]
			if row == nil {
				row = &ProfileRouteRow{Route: cost.Route}
				rows[cost.Route] = row
			}
			copy := cost
			if baseline {
				row.Baseline = &copy
			} else {
				row.Candidate = &copy
			}
		}
	}
	if comparison != nil {
		add(&comparison.Baseline, true)
		add(&comparison.Candidate, false)
		v.Adjustment = comparison.RouteAdjustment
		v.Attribution = comparison.Attribution
	} else {
		add(p, false)
		if p != nil {
			v.CandidateAttribution = p.Attribution
		}
	}
	for route, row := range rows {
		values := url.Values{"route": {route}, "deployment_id": {q.DeploymentID}, "runtime": {q.Runtime}, "start": {q.Start.Format(time.RFC3339Nano)}, "end": {q.End.Format(time.RFC3339Nano)}}
		if b.DeploymentID != "" {
			values.Set("baseline_id", b.DeploymentID)
			values.Set("baseline_start", b.Start.Format(time.RFC3339Nano))
			values.Set("baseline_end", b.End.Format(time.RFC3339Nano))
		}
		anchor := "#flamegraph"
		if b.DeploymentID != "" {
			anchor = "#diff-flamegraph"
		}
		row.URL = "/dashboard/apps/" + url.PathEscape(slug) + "/profiles?" + values.Encode() + anchor
		if row.Baseline != nil && row.Candidate != nil && row.Baseline.CPUSecondsPerRequest != nil && row.Candidate.CPUSecondsPerRequest != nil {
			delta := *row.Candidate.CPUSecondsPerRequest - *row.Baseline.CPUSecondsPerRequest
			row.DeltaCPUSecondsPerRequest = &delta
		}
		v.Rows = append(v.Rows, *row)
	}
	sort.Slice(v.Rows, func(i, j int) bool {
		value := func(r ProfileRouteRow) float64 {
			if r.Candidate != nil {
				return r.Candidate.CPUSeconds
			}
			if r.Baseline != nil {
				return r.Baseline.CPUSeconds
			}
			return 0
		}
		a, b := value(v.Rows[i]), value(v.Rows[j])
		if a != b {
			return a > b
		}
		return v.Rows[i].Route < v.Rows[j].Route
	})
	return v
}
