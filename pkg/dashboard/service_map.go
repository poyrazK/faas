package dashboard

import "github.com/onebox-faas/faas/pkg/api"

// ServiceMapData is the /dashboard/service-map page (ADR-732). Map is nil
// when the preview is disabled, the plan lacks metrics, or the range is
// invalid; the template explains which.
type ServiceMapData struct {
	Enabled     bool
	PlanAllowed bool
	Range       string
	Ranges      []string
	Map         *api.ServiceMapResponse
	// HighErrorRatePct highlights edges at or above this error rate.
	HighErrorRatePct float64
	// Degraded is the reason from a "degraded: <reason>" Source, empty when
	// the map came from Prometheus.
	Degraded string
}
