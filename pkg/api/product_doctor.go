package api

import "time"

// Doctor observes configuration on the responding API node. It never grants
// admission, verifies live Auth membership, or qualifies a native execution lane.
type ProductDoctorReport struct {
	AppID              string               `json:"app_id"`
	TenantID           string               `json:"tenant_id,omitempty"`
	Revision           string               `json:"revision,omitempty"`
	HeadRevision       string               `json:"head_revision,omitempty"`
	ObservedAt         time.Time            `json:"observed_at"`
	ObservationScope   string               `json:"observation_scope"`
	ConfigurationState string               `json:"configuration_state"`
	QualificationState string               `json:"qualification_state"`
	Checks             []ProductDoctorCheck `json:"checks"`
}

type ProductDoctorCheck struct {
	Check       string `json:"check"`
	Feature     string `json:"feature,omitempty"`
	Status      string `json:"status"`
	Impact      string `json:"impact"`
	Code        string `json:"code"`
	Message     string `json:"message"`
	Remediation string `json:"remediation,omitempty"`
}

func (r ProductDoctorReport) ObservedConfigurationState() string {
	state := "observed"
	if len(r.Checks) == 0 {
		return "unverified"
	}
	for _, c := range r.Checks {
		if c.Impact == "qualification" {
			continue
		}
		if c.Status == "blocked" {
			return "blocked"
		}
		switch c.Status {
		case "observed", "configured", "not_requested":
		default:
			state = "unverified"
		}
	}
	return state
}
