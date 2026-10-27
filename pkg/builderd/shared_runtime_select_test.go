package builderd

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// spec: §4.6
// H10-140: only Node container apps without a runtime, a Dockerfile or job
// mode build on a shared runner base, unless the node turns it off
// (default on, ADR-20261028090000).
func TestSharedSourceAppRuntime(t *testing.T) {
	on := func(string) string { return "" }
	off := func(string) string { return "0" }
	app := state.App{Type: state.AppTypeApp}
	job := app
	job.Manifest.ExecutionMode = api.ExecutionModeJob
	tests := []struct {
		name       string
		app        state.App
		fw         Framework
		nodeSpec   string
		dockerfile string
		env        func(string) string
		want       string
	}{
		{"node app defaults to node24", app, FrameworkNode, "", "", on, "node24"},
		{"engines 22.11.0", app, FrameworkNode, "22.11.0", "", on, "node22"},
		{"engines bare 22", app, FrameworkNode, "22", "", on, "node22"},
		{"engines 24", app, FrameworkNode, "24", "", on, "node24"},
		{"declared 20 keeps its toolchain", app, FrameworkNode, "20", "", on, ""},
		{"flag off", app, FrameworkNode, "", "", off, ""},
		{"explicit on", app, FrameworkNode, "22", "", func(string) string { return "1" }, "node22"},
		{"function", state.App{Type: state.AppTypeFunction, Runtime: "node22"}, FrameworkNode, "", "", on, ""},
		{"own runtime", state.App{Type: state.AppTypeApp, Runtime: "go124"}, FrameworkNode, "", "", on, ""},
		{"python", app, FrameworkPython, "", "", on, ""},
		{"dockerfile", app, FrameworkNode, "", "Dockerfile", on, ""},
		{"job mode", job, FrameworkNode, "", "", on, ""},
	}
	for _, tt := range tests {
		if got := sharedSourceAppRuntime(tt.app, tt.fw, tt.nodeSpec, tt.dockerfile, tt.env); got != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestClaimedBuildRuntime(t *testing.T) {
	app := state.App{Type: state.AppTypeApp}
	if got := claimedBuildRuntime(app, BuildResult{Runtime: "node24"}); got != "node24" {
		t.Fatalf("shared build claims %q, want node24", got)
	}
	if got := claimedBuildRuntime(app, BuildResult{}); got != "" {
		t.Fatalf("plain build claims %q, want the app's runtime", got)
	}
	fn := state.App{Type: state.AppTypeFunction, Runtime: "node22"}
	if got := claimedBuildRuntime(fn, BuildResult{Runtime: "node24"}); got != "node22" {
		t.Fatalf("function claims %q, want its own runtime", got)
	}
}
