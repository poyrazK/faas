package sched

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261119140000: readiness probes of a production deployment name the
// app's public host once the switch is on; everything else keeps the legacy
// instance-address probe.
func TestProbeHostFor(t *testing.T) {
	app := state.App{Slug: "my-site"}
	tests := []struct {
		name       string
		enable     bool
		appsDomain string
		slug       string
		scope      string
		want       string
	}{
		{name: "switch off", slug: "my-site", want: ""},
		{name: "default scope", enable: true, appsDomain: "gregale.dev", slug: "my-site", want: "my-site.gregale.dev"},
		{name: "production scope", enable: true, appsDomain: "gregale.dev", slug: "my-site", scope: "production", want: "my-site.gregale.dev"},
		{name: "leading dot domain", enable: true, appsDomain: ".apps.example.test", slug: "my-site", want: "my-site.apps.example.test"},
		{name: "staging scope keeps legacy", enable: true, appsDomain: "gregale.dev", slug: "my-site", scope: "staging", want: ""},
		{name: "custom-domain-only operator", enable: true, appsDomain: "", slug: "my-site", want: ""},
		{name: "slug that is no host label", enable: true, appsDomain: "gregale.dev", slug: "a.b", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &Engine{}
			if tt.enable {
				e.EnableReadinessProbeAppHost(tt.appsDomain)
			}
			app.Slug = tt.slug
			if got := e.probeHostFor(app, state.Deployment{Scope: tt.scope}); got != tt.want {
				t.Fatalf("probeHostFor = %q, want %q", got, tt.want)
			}
		})
	}
}
