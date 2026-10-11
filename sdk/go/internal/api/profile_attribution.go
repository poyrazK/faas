package api

// Fractions describe sampled CPU in the whole deployment window, not VM CPU,
// request instrumentation completeness or the selected route's execution.
type ProfileAttributionQuality struct {
	Available              bool                       `json:"available"`
	TotalCPUSeconds        float64                    `json:"total_cpu_seconds"`
	AttributedCPUSeconds   float64                    `json:"attributed_cpu_seconds"`
	UnattributedCPUSeconds float64                    `json:"unattributed_cpu_seconds"`
	AttributedPercent      *float64                   `json:"attributed_percent,omitempty"`
	UnattributedPercent    *float64                   `json:"unattributed_percent,omitempty"`
	DiagnosticsComplete    bool                       `json:"diagnostics_complete"`
	Reasons                []ProfileAttributionReason `json:"reasons"`
}

type ProfileAttributionReason struct {
	Reason     string  `json:"reason"`
	CPUSeconds float64 `json:"cpu_seconds"`
}

type ProfileAttributionComparison struct {
	Baseline                      *ProfileAttributionQuality `json:"baseline,omitempty"`
	Candidate                     *ProfileAttributionQuality `json:"candidate,omitempty"`
	Available                     bool                       `json:"available"`
	DeltaPercentagePoints         *float64                   `json:"delta_percentage_points,omitempty"`
	SubstantialChange             bool                       `json:"substantial_change"`
	MaximumChangePercentagePoints float64                    `json:"maximum_change_percentage_points"`
	Warnings                      []string                   `json:"warnings"`
}
