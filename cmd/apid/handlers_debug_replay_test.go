package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

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
