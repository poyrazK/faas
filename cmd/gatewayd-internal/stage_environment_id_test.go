package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261104120000 — only a stage's host is counted per environment; a
// production environment's host is not, so production's alert rules can
// subtract the per-environment series.
func TestStageEnvironmentID(t *testing.T) {
	for _, tc := range []struct{ slug, want string }{
		{"staging", "env-1"},
		{"preview-42", "env-1"},
		{"production", ""},
		{state.DefaultEnvScope, ""},
		{"", ""},
	} {
		if got := stageEnvironmentID(state.ProjectEnvironment{ID: "env-1", Slug: tc.slug}); got != tc.want {
			t.Errorf("slug %q: %q, want %q", tc.slug, got, tc.want)
		}
	}
}
