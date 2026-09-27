package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestDebugReplayRequestPath(t *testing.T) {
	tests := []struct {
		name   string
		method string
		route  string
		want   string
		ok     bool
	}{
		{name: "method-prefixed normalized route", method: "GET", route: "GET /profiles/{id}", want: "/profiles/{id}", ok: true},
		{name: "legacy path", method: "GET", route: "/health", want: "/health", ok: true},
		{name: "overflow route", method: "GET", route: "__route_other__"},
		{name: "absolute URL", method: "GET", route: "https://example.test/health"},
		{name: "network path", method: "GET", route: "//example.test/health"},
		{name: "wrong method prefix", method: "GET", route: "POST /health"},
		{name: "query string", method: "GET", route: "/health?token=secret"},
		{name: "fragment", method: "GET", route: "/health#details"},
		{name: "missing method", route: "/health"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := debugReplayRequestPath(tt.method, tt.route)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("debugReplayRequestPath(%q, %q) = (%q, %t), want (%q, %t)", tt.method, tt.route, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestSelectDebugReplayMirrorRule(t *testing.T) {
	rules := []state.MirrorRule{
		{ID: "disabled", SourceDeploymentID: "source", MirrorDeploymentID: "disabled-target", Enabled: false},
		{ID: "first", SourceDeploymentID: "source", MirrorDeploymentID: "target-a", Enabled: true},
		{ID: "second", SourceDeploymentID: "source", MirrorDeploymentID: "target-b", Enabled: true},
		{ID: "other-source", SourceDeploymentID: "other", MirrorDeploymentID: "target-b", Enabled: true},
	}

	t.Run("default selects first enabled target for source", func(t *testing.T) {
		got, ok := selectDebugReplayMirrorRule(rules, "source", "")
		if !ok || got.ID != "first" {
			t.Fatalf("rule = %#v, ok=%t; want first enabled source rule", got, ok)
		}
	})

	t.Run("requested target selects matching rule", func(t *testing.T) {
		got, ok := selectDebugReplayMirrorRule(rules, "source", "target-b")
		if !ok || got.ID != "second" {
			t.Fatalf("rule = %#v, ok=%t; want second target rule", got, ok)
		}
	})

	t.Run("requested target cannot cross source deployment", func(t *testing.T) {
		if got, ok := selectDebugReplayMirrorRule(rules, "source", "missing"); ok || got.ID != "" {
			t.Fatalf("rule = %#v, ok=%t; want no matching rule", got, ok)
		}
	})
}
