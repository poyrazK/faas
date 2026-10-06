package gateway

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/gateway/activity"
)

// WithDeploymentActivity wraps the common VM forwarding factory (ADR-610).
// HTTP, synthetic invocations, mirrors and raw upgrades share this seam. The
// bridge returns only after its body pumps or hijacked connection end. Preserve
// the original ResponseWriter so streaming, Flush and Hijack remain available.
// A nil tracker leaves the factory unchanged; wiring is private and default off.
func WithDeploymentActivity(factory func(Target) http.Handler, tracker *activity.Tracker) func(Target) http.Handler {
	if tracker == nil {
		return factory
	}
	return func(target Target) http.Handler {
		forward := factory(target)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer tracker.Begin(target.AppID, target.DeploymentID)()
			forward.ServeHTTP(w, r)
		})
	}
}
