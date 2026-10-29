package deploydiff

import (
	"bytes"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestDiffCompanions is Hunt 14 H14-53's follow-up: deploy --dry-run showed
// nothing about companions, although a companion port opens a public
// <app>--<name> hostname.
func TestDiffCompanions(t *testing.T) {
	live := &api.DeploymentResponse{Companions: []api.DeploymentCompanion{
		{Name: "otel", Preset: "opentelemetry", Image: "registry.example/otel@sha256:aa", Type: "sidecar", Port: 4318, RAMMB: 64},
		{Name: "proxy", Image: "registry.example/proxy@sha256:bb", Type: "sidecar", Port: 8081, RAMMB: 128},
		{Name: "metrics", Image: "registry.example/metrics@sha256:cc", Type: "sidecar", RAMMB: 32},
	}}
	app := &api.AppResponse{URL: "https://shop.gregale.dev"}
	for _, tc := range []struct {
		name    string
		pending Pending
		want    []string // "kind field"
		text    []string
	}{
		{name: "manifest not read", pending: Pending{}},
		{
			name: "unchanged, including a preset the server filled in",
			pending: Pending{CompanionsKnown: true, Companions: []api.DeploymentCompanion{
				{Name: "otel", Preset: "opentelemetry"},
				{Name: "proxy", Image: "registry.example/proxy@sha256:bb", Port: 8081},
				{Name: "metrics", Image: "registry.example/metrics@sha256:cc"},
			}},
		},
		{
			name: "new public companion, port change, removal",
			pending: Pending{CompanionsKnown: true, Companions: []api.DeploymentCompanion{
				{Name: "otel", Preset: "opentelemetry"},
				{Name: "proxy", Image: "registry.example/proxy@sha256:bb", Port: 9090},
				{Name: "web", Image: "registry.example/nginx@sha256:dd", Port: 8080},
			}},
			want: []string{"modify companion.proxy", "add companion.web", "remove companion.metrics"},
			text: []string{
				"Companions:",
				"+ web  image=registry.example/nginx@sha256:dd port=8080 public at shop--web.gregale.dev",
				"~ proxy  image=registry.example/proxy@sha256:bb port=8081 ram=128MB public at shop--proxy.gregale.dev → image=registry.example/proxy@sha256:bb port=9090 public at shop--proxy.gregale.dev",
				"- metrics  image=registry.example/metrics@sha256:cc ram=32MB",
			},
		},
		{
			name:    "no manifest removes every companion",
			pending: Pending{CompanionsKnown: true},
			want:    []string{"remove companion.metrics", "remove companion.otel", "remove companion.proxy"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := Compute("shop", "", Baseline{App: app, LatestDeployment: live}, tc.pending)
			var got []string
			for _, c := range d.Changes {
				if strings.HasPrefix(c.Field, "companion.") {
					got = append(got, string(c.Kind)+" "+c.Field)
				}
			}
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Fatalf("companion changes = %q, want %q", got, tc.want)
			}
			var out bytes.Buffer
			RenderText(&out, d)
			for _, line := range tc.text {
				if !strings.Contains(out.String(), line) {
					t.Errorf("rendered diff lacks %q:\n%s", line, out.String())
				}
			}
		})
	}
}

func TestNewAppCompanionHostIsNamedWithoutTheDomain(t *testing.T) {
	d := Compute("fresh", "", Baseline{}, Pending{CompanionsKnown: true, Companions: []api.DeploymentCompanion{
		{Name: "web", Image: "registry.example/nginx@sha256:dd", Port: 8080},
	}})
	var out bytes.Buffer
	RenderText(&out, d)
	if !strings.Contains(out.String(), "+ web  image=registry.example/nginx@sha256:dd port=8080 public at <app>--web.<apps domain>") {
		t.Fatalf("a new app's companion port is not called public:\n%s", out.String())
	}
}
