package api

// Guest execution evidence headers are emitted by the platform-owned runner
// and consumed by the gateway. They are intentionally internal: the gateway
// strips them before returning a response to a customer, while retaining the
// bounded values for debugger telemetry.
const (
	GuestEvidenceDurationHeader = "X-Faas-Guest-Duration-Ms"
	// UsageHeader carries the billable units an app reports for a request,
	// as comma-separated meter=units pairs (ADR-1318). The gateway records
	// and removes it, as a header or as a trailer of a streamed response.
	UsageHeader                   = "Gregale-Usage"
	GuestEvidenceRuntimeHeader    = "X-Faas-Guest-Runtime"
	GuestEvidenceOutcomeHeader    = "X-Faas-Guest-Outcome"
	GuestEvidenceErrorClassHeader = "X-Faas-Guest-Error-Class"
	GuestEvidenceCPUTimeHeader    = "X-Faas-Guest-Cpu-Time-Ms"
	GuestEvidencePeakRSSHeader    = "X-Faas-Guest-Peak-Rss-Mb"
)
