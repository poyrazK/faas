package api

// Guest execution evidence headers are emitted by the platform-owned runner
// and consumed by the gateway. They are intentionally internal: the gateway
// strips them before returning a response to a customer, while retaining the
// bounded values for debugger telemetry.
const (
	GuestEvidenceDurationHeader   = "X-Faas-Guest-Duration-Ms"
	GuestEvidenceRuntimeHeader    = "X-Faas-Guest-Runtime"
	GuestEvidenceOutcomeHeader    = "X-Faas-Guest-Outcome"
	GuestEvidenceErrorClassHeader = "X-Faas-Guest-Error-Class"
)
