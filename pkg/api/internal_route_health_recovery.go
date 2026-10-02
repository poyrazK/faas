package api

// These DTOs are exclusive to the loopback Safe Deploy action listener. The
// caller pins a stage; APID selects and validates current policy and evidence.
type CanaryRouteHealthRecoveryRequest struct {
	ExpectedStep int `json:"expected_step"`
}
type CanaryRouteHealthRecoveryResponse struct {
	Aborted     bool                 `json:"aborted"`
	RouteHealth *RouteHealthDecision `json:"route_health,omitempty"`
	AuditID     string               `json:"audit_id,omitempty"`
}
