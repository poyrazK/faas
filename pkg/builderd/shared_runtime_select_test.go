package builderd

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// spec: §4.6
// H10-140: only Node and Python container apps without a runtime, a
// Dockerfile or job mode build on a shared runner base, unless the node turns
// it off (default on, ADR-20261029103512; Python: ADR-1500 amendment 2).
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
		specs      []string
		dockerfile string
		env        func(string) string
		want       string
	}{
		{"node app defaults to node24", app, FrameworkNode, nil, "", on, "node24"},
		// runner-node22 is musl; the glibc toolchain keeps its own base.
		{"engines 22.11.0", app, FrameworkNode, []string{"22.11.0"}, "", on, ""},
		{"engines bare 22", app, FrameworkNode, []string{"22"}, "", on, ""},
		{"lower bound 22", app, FrameworkNode, []string{">=22"}, "", on, "node24"},
		{"engines 24", app, FrameworkNode, []string{"24"}, "", on, "node24"},
		{"declared 20 keeps its toolchain", app, FrameworkNode, []string{"20"}, "", on, ""},
		{"flag off", app, FrameworkNode, nil, "", off, ""},
		{"explicit on", app, FrameworkNode, []string{"24"}, "", func(string) string { return "1" }, "node24"},
		{"function", state.App{Type: state.AppTypeFunction, Runtime: "node22"}, FrameworkNode, nil, "", on, ""},
		{"own runtime", state.App{Type: state.AppTypeApp, Runtime: "go124"}, FrameworkNode, nil, "", on, ""},
		{"python app defaults to python313", app, FrameworkPython, nil, "", on, "python313"},
		{"python 3.12", app, FrameworkPython, []string{"3.12"}, "", on, "python312"},
		{"python declarations disagree", app, FrameworkPython, []string{"3.12", "python-3.13.1"}, "", on, ""},
		{"declared python 3.11 keeps its toolchain", app, FrameworkPython, []string{"3.11"}, "", on, ""},
		{"python flag off", app, FrameworkPython, nil, "", off, ""},
		{"railpack.json pins node 20 beside engines 24", app, FrameworkNode, []string{"24", "20"}, "", on, ""},
		{"engines and railpack.json agree", app, FrameworkNode, []string{"24", "24"}, "", on, "node24"},
		{"go", app, FrameworkGo, nil, "", on, ""},
		{"dockerfile", app, FrameworkNode, nil, "Dockerfile", on, ""},
		{"job mode", job, FrameworkNode, nil, "", on, ""},
	}
	for _, tt := range tests {
		if got := sharedSourceAppRuntime(tt.app, tt.fw, tt.specs, tt.dockerfile, tt.env); got != tt.want {
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
