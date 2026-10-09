package api

import (
	"strings"
	"unicode/utf8"
)

const ProfileRouteLabel = "gregale_route"
const ProfileUnattributedRoute = "[unattributed]"

func ValidProfileRoute(route string) bool {
	if route == "" || route == ProfileUnattributedRoute {
		return true
	}
	if len(route) > ProfileRouteMaxLabelBytes || !utf8.ValidString(route) || strings.IndexFunc(route, func(r rune) bool { return r < 32 || r == 127 }) >= 0 {
		return false
	}
	method, path, ok := strings.Cut(route, " ")
	if !ok || !strings.HasPrefix(path, "/") || strings.ContainsAny(path, " ?#") {
		return false
	}
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
		return true
	}
	return false
}

type ProfileRouteCPU struct {
	LabelCoverage        *ProfileRouteLabelCoverage `json:"label_coverage,omitempty"`
	Route                string                     `json:"route"`
	CPUSeconds           float64                    `json:"cpu_seconds"`
	Requests             *int64                     `json:"requests,omitempty"`
	CPUSecondsPerRequest *float64                   `json:"cpu_seconds_per_request,omitempty"`
}

type ProfileRouteAdjustment struct {
	Available                     bool                 `json:"available"`
	Reason                        string               `json:"reason"`
	BaselineCPUSecondsPerRequest  *float64             `json:"baseline_cpu_seconds_per_request,omitempty"`
	CandidateCPUSecondsPerRequest *float64             `json:"candidate_cpu_seconds_per_request,omitempty"`
	DeltaCPUSecondsPerRequest     *float64             `json:"delta_cpu_seconds_per_request,omitempty"`
	Weights                       []ProfileRouteWeight `json:"weights"`
}

type ProfileRouteWeight struct {
	Route  string  `json:"route"`
	Weight float64 `json:"weight"`
}
