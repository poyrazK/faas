package api

// Counts are application-reported request entries during retained captures,
// compared with weighted observed traffic in the whole deployment window.
type ProfileRouteLabelCoverage struct {
	Available        bool     `json:"available"`
	Reason           string   `json:"reason"`
	LabeledRequests  *int64   `json:"labeled_requests,omitempty"`
	ObservedRequests *int64   `json:"observed_requests,omitempty"`
	Percent          *float64 `json:"percent,omitempty"`
	CapturedProfiles int64    `json:"captured_profiles"`
	BoundaryProfiles int64    `json:"boundary_profiles"`
}

type ProfileRouteLabelComparison struct {
	Baseline                      *ProfileRouteLabelCoverage `json:"baseline,omitempty"`
	Candidate                     *ProfileRouteLabelCoverage `json:"candidate,omitempty"`
	Available                     bool                       `json:"available"`
	Consistent                    bool                       `json:"consistent"`
	Reason                        string                     `json:"reason"`
	DeltaPercentagePoints         *float64                   `json:"delta_percentage_points,omitempty"`
	MinimumPercent                float64                    `json:"minimum_percent"`
	MaximumChangePercentagePoints float64                    `json:"maximum_change_percentage_points"`
}
