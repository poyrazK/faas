package dashboard

import (
	"fmt"
	"math"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type ProfileRequestMixView struct {
	CapturedAt                          string
	Completeness                        string
	Frozen                              bool
	BaselineCaptured, CandidateCaptured bool
	BaselineWindow, CandidateWindow     string
	Message                             string
	Warnings                            []string
	BaselineTotal, CandidateTotal       int64
	Routes, Statuses                    []ProfileRequestMixRow
	RouteDifference, StatusDifference   *float64
}

func ProfileRequestMixSnapshotView(s *api.ProfileRequestMixSnapshot, minimum int64) ProfileRequestMixView {
	if s == nil {
		return ProfileRequestMixView{Message: "No request-mix snapshot was recorded."}
	}
	v := ProfileRequestMixView{Message: s.Reason, Warnings: s.Warnings}
	if s.Baseline != nil && s.Candidate != nil {
		a, b := s.Baseline, s.Candidate
		v = BuildProfileRequestMix(state.ProfileRequestMix{Total: a.Total, Routes: a.Routes, Statuses: a.Statuses, Truncated: a.Truncated}, state.ProfileRequestMix{Total: b.Total, Routes: b.Routes, Statuses: b.Statuses, Truncated: b.Truncated}, minimum)
		v.Message = s.Reason
		v.Warnings = s.Warnings
		v.RouteDifference = s.RouteDifference
		v.StatusDifference = s.StatusDifference
	}
	v.Frozen = true
	if s.Baseline != nil {
		v.BaselineCaptured = true
		v.BaselineTotal = s.Baseline.Total
	}
	if s.Candidate != nil {
		v.CandidateCaptured = true
		v.CandidateTotal = s.Candidate.Total
	}
	window := func(w *api.ProfileRequestMixWindow) string {
		if w == nil {
			return "Not captured"
		}
		return w.Query.DeploymentID + ": " + w.Query.Start.Format("2006-01-02T15:04:05.999999999Z07:00") + " – " + w.Query.End.Format("2006-01-02T15:04:05.999999999Z07:00")
	}
	v.BaselineWindow = window(s.Baseline)
	v.CandidateWindow = window(s.Candidate)
	v.CapturedAt = s.CapturedAt.Format("2006-01-02T15:04:05.999999999Z07:00")
	v.Completeness = s.Status
	if s.Complete {
		v.Completeness = "All observed route and status groups captured"
	}
	return v
}

type ProfileRequestMixRow struct {
	Label, Method, BaselineURL, CandidateURL  string
	BaselineCount, CandidateCount             *int64
	BaselineShare, CandidateShare, Difference *float64
}

func BuildProfileRequestMix(a, b state.ProfileRequestMix, minimum int64) ProfileRequestMixView {
	v := ProfileRequestMixView{Message: "Retained telemetry for the recorded deployment windows", BaselineTotal: a.Total, CandidateTotal: b.Total}
	if a.Total <= 0 || b.Total <= 0 {
		v.Warnings = append(v.Warnings, "Request-mix comparison is unavailable: one or both windows have no retained requests.")
		return v
	}
	sufficient := a.Total >= minimum && b.Total >= minimum
	if !sufficient {
		v.Warnings = append(v.Warnings, fmt.Sprintf("Request traffic is below the recorded minimum of %d requests per window; mix warnings are suppressed.", minimum))
	}
	if a.Truncated || b.Truncated {
		v.Warnings = append(v.Warnings, "Route summaries are truncated. Missing routes on a truncated side are unknown; route-mix difference is unavailable.")
	}
	v.Routes, v.RouteDifference = compareRequestMixGroups(a.Routes, b.Routes, a.Total, b.Total, a.Truncated, b.Truncated)
	v.Statuses, v.StatusDifference = compareRequestMixGroups(a.Statuses, b.Statuses, a.Total, b.Total, false, false)
	for _, dimension := range []struct {
		name  string
		value *float64
	}{{"Route", v.RouteDifference}, {"Response-status", v.StatusDifference}} {
		if sufficient && dimension.value != nil && *dimension.value >= api.ProfileRequestMixDifferencePercent {
			v.Warnings = append(v.Warnings, fmt.Sprintf("%s mix differs substantially: %.1f percentage points of traffic share shifted (warning threshold %.0f). CPU/request comparisons may be affected by request mix.", dimension.name, *dimension.value, api.ProfileRequestMixDifferencePercent))
		}
	}
	return v
}

func compareRequestMixGroups(a, b []state.ProfileRequestMixGroup, totalA, totalB int64, truncatedA, truncatedB bool) ([]ProfileRequestMixRow, *float64) {
	type key struct{ label, method string }
	left, right := map[key]int64{}, map[key]int64{}
	keys := map[key]bool{}
	for _, g := range a {
		k := key{g.Label, g.Method}
		left[k] = g.Requests
		keys[k] = true
	}
	for _, g := range b {
		k := key{g.Label, g.Method}
		right[k] = g.Requests
		keys[k] = true
	}
	rows := make([]ProfileRequestMixRow, 0, len(keys))
	distance := 0.0
	for k := range keys {
		row := ProfileRequestMixRow{Label: k.label, Method: k.method}
		countA, knownA := left[k]
		countB, knownB := right[k]
		if knownA || !truncatedA {
			share := 100 * float64(countA) / float64(totalA)
			row.BaselineCount = &countA
			row.BaselineShare = &share
		}
		if knownB || !truncatedB {
			share := 100 * float64(countB) / float64(totalB)
			row.CandidateCount = &countB
			row.CandidateShare = &share
		}
		if row.BaselineShare != nil && row.CandidateShare != nil {
			delta := *row.CandidateShare - *row.BaselineShare
			row.Difference = &delta
			distance += math.Abs(delta) / 2
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Label != rows[j].Label {
			return rows[i].Label < rows[j].Label
		}
		return rows[i].Method < rows[j].Method
	})
	if truncatedA || truncatedB {
		return rows, nil
	}
	return rows, &distance
}
