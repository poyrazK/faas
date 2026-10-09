package api

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
