package sched

import (
	"github.com/onebox-faas/faas/pkg/hostidentity"
	"github.com/onebox-faas/faas/pkg/state"
)

// Readiness probes name the app's public host (ADR-20261119140000).
//
// vmmd's readiness and prime GETs reach the guest at its instance address, so
// an app that allow-lists its host names (Django ALLOWED_HOSTS, Rails
// config.hosts) rejected every probe with 400 even when configured exactly as
// docs/deploy-django.md says, and its first deploy timed out. With the switch
// on, schedd hands vmmd the host public traffic arrives on, and the probe
// looks like that traffic.

// EnableReadinessProbeAppHost turns on the public Host for readiness probes
// (schedd `readiness_probe_app_host`, off by default). appsDomain is the
// public apps domain (`apps_domain`); an empty domain (custom-domain-only
// operators) leaves the legacy probe in place.
func (e *Engine) EnableReadinessProbeAppHost(appsDomain string) {
	suffix := hostidentity.AppsSuffix(appsDomain)
	if suffix == "" {
		e.probeHostAppsSuffix.Store(nil)
		return
	}
	e.probeHostAppsSuffix.Store(&suffix)
}

// probeHostFor returns the Host vmmd's readiness and prime GETs send for a
// deployment of app: `<slug><apps suffix>` for a production-scope deployment,
// the host the app's own settings must already allow for its traffic. It is
// empty, keeping the legacy instance-address probe, when the switch is off,
// for an environment or preview scope (served on other hosts), and for a slug
// that does not form a valid public host.
func (e *Engine) probeHostFor(app state.App, dep state.Deployment) string {
	suffix := e.probeHostAppsSuffix.Load()
	if suffix == nil || !state.ProductionDeploymentScope(dep.Scope) {
		return ""
	}
	return hostidentity.BuildPrimaryAppHost(*suffix, app.Slug)
}
