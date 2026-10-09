package api

import "time"

// Doctor describes one responding API node at observation time. It never
// reserves quota, grants admission or attests runtime/fleet qualification.
type OperationDoctorResponse struct {
	AppID            string                 `json:"app_id"`
	Scope            string                 `json:"scope"`
	DeploymentID     string                 `json:"deployment_id"`
	PlatformTenantID string                 `json:"platform_tenant_id"`
	Plan             Plan                   `json:"plan"`
	ObservedAt       time.Time              `json:"observed_at"`
	ObservationScope string                 `json:"observation_scope"`
	SubmissionState  string                 `json:"submission_state"`
	Checks           []OperationDoctorCheck `json:"checks"`
}

type OperationDoctorCheck struct {
	Check         string `json:"check"`
	Status        string `json:"status"`
	Impact        string `json:"impact"`
	Code          string `json:"code"`
	Message       string `json:"message"`
	Remediation   string `json:"remediation,omitempty"`
	DefinitionID  string `json:"definition_id,omitempty"`
	Name          string `json:"name,omitempty"`
	Revision      string `json:"revision,omitempty"`
	ReleaseID     string `json:"release_id,omitempty"`
	ExecutionKind string `json:"execution_kind,omitempty"`
	Limit         *int64 `json:"limit,omitempty"`
	Observed      *int64 `json:"observed,omitempty"`
}

// SubmissionState considers submission checks only. A delivery warning never
// requires another execution. Unknown prerequisites must not report eligibility.
func (r OperationDoctorResponse) ObservedSubmissionState() string {
	state := "eligible"
	for _, check := range r.Checks {
		if check.Impact != "submission" {
			continue
		}
		if check.Status == "blocked" {
			return "blocked"
		}
		if check.Status == "unknown" {
			state = "unknown"
		}
	}
	return state
}
