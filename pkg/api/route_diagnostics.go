package api

// RouteDiagnosticsIdentity is set only for explicit environment selectors.
// Historical evidence without a retained, matching deployment is excluded.
type RouteDiagnosticsIdentity struct {
	AppID         string `json:"app_id,omitempty"`
	Scope         string `json:"scope,omitempty"`
	ProjectID     string `json:"project_id,omitempty"`
	EnvironmentID string `json:"environment_id,omitempty"`
	Coverage      string `json:"coverage,omitempty"`
	Partial       bool   `json:"partial,omitempty"`
}
