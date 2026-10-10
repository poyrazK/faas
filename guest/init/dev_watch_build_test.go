package main

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/devpatch"
)

// adr: 970 — the watch overlay sets the development command and marks the
// image, and only Node builds accept it.
func TestDevWatchRailpackConfig(t *testing.T) {
	m := api.BuildManifest{Framework: api.FrameworkRailpackNode, DevWatchCommand: "npm run dev"}
	config := map[string]any{"deploy": map[string]any{"base": map[string]any{"image": "runner"}}}
	if err := devWatchRailpackConfig(config, m); err != nil {
		t.Fatal(err)
	}
	deploy := config["deploy"].(map[string]any)
	if deploy["startCommand"] != "npm run dev" || deploy["base"].(map[string]any)["image"] != "runner" {
		t.Fatalf("deploy = %+v", deploy)
	}
	variables := deploy["variables"].(map[string]any)
	if variables["NODE_ENV"] != "development" || variables[api.DevWatchEnv] != "1" {
		t.Fatalf("variables = %+v", variables)
	}
	if err := devWatchRailpackConfig(map[string]any{}, api.BuildManifest{Framework: api.FrameworkRailpackPython, DevWatchCommand: "uvicorn app:app --reload"}); err == nil ||
		!strings.HasPrefix(err.Error(), api.CodeDevWatchUnsupported) {
		t.Fatalf("python watch build error = %v, want %s", err, api.CodeDevWatchUnsupported)
	}
	if err := devWatchRailpackConfig(map[string]any{"steps": "not an object"}, m); err == nil {
		t.Fatal("a malformed railpack.json was accepted")
	}
	untouched := map[string]any{}
	if err := devWatchRailpackConfig(untouched, api.BuildManifest{Framework: api.FrameworkRailpackNode}); err != nil || len(untouched) != 0 {
		t.Fatalf("a normal build changed the overlay: %+v, %v", untouched, err)
	}
}

// The overlay's build commands must turn Railpack's real Node plan with a
// build script into a verbatim one, which is what makes live patches apply.
func TestDevWatchOverlayMakesTheNodeBuildPlanVerbatim(t *testing.T) {
	data, err := os.ReadFile("../../pkg/devpatch/testdata/railpack-0.38.0-node-build.json")
	if err != nil {
		t.Fatal(err)
	}
	if got := devpatch.ClassifyRailpackPlan(data); got.Verbatim || got.Reason != api.DevPatchReasonBuildCommand {
		t.Fatalf("fixture baseline = %+v, want build_command", got)
	}
	config := map[string]any{}
	if err := devWatchRailpackConfig(config, api.BuildManifest{Framework: api.FrameworkRailpackNode, DevWatchCommand: "npm run dev"}); err != nil {
		t.Fatal(err)
	}
	overrides := config["steps"].(map[string]any)["build"].(map[string]any)["commands"]
	var plan map[string]any
	if err := json.Unmarshal(data, &plan); err != nil {
		t.Fatal(err)
	}
	for _, step := range plan["steps"].([]any) {
		if s := step.(map[string]any); s["name"] == "build" {
			s["commands"] = overrides
		}
	}
	rewritten, _ := json.Marshal(plan)
	got := devpatch.ClassifyRailpackPlan(rewritten)
	if !got.Verbatim {
		t.Fatalf("watch plan = %+v, want verbatim", got)
	}
	watched, err := devWatchSourceMap(api.BuildManifest{DevWatchCommand: "npm run dev"}, &got)
	if err != nil || !watched.Watch || !watched.Verbatim {
		t.Fatalf("watch source map = %+v, %v", watched, err)
	}
}

func TestDevWatchSourceMap(t *testing.T) {
	plain := &api.DevPatchSourceMap{Version: 1, Reason: api.DevPatchReasonBuildCommand}
	if got, err := devWatchSourceMap(api.BuildManifest{}, plain); err != nil || got != plain {
		t.Fatalf("normal build = %+v, %v; want untouched", got, err)
	}
	if _, err := devWatchSourceMap(api.BuildManifest{DevWatchCommand: "npm run dev"}, plain); err == nil ||
		!strings.Contains(err.Error(), api.CodeDevWatchUnsupported) || !strings.Contains(err.Error(), api.DevPatchReasonBuildCommand) {
		t.Fatalf("non-verbatim watch build error = %v", err)
	}
	if _, err := devWatchSourceMap(api.BuildManifest{DevWatchCommand: "npm run dev"}, nil); err == nil {
		t.Fatal("a watch build without a source map passed")
	}
}

func TestDevPatchRestartSkippedInWatchMode(t *testing.T) {
	t.Cleanup(func() { devWatchActive.Store(false) })
	calls := 0
	restart := devPatchRestart(func() error { calls++; return errors.New("restarted") })
	devWatchActive.Store(true)
	if err := restart(); err != nil || calls != 0 {
		t.Fatalf("watch mode restarted the workload: calls=%d err=%v", calls, err)
	}
	devWatchActive.Store(false)
	if err := restart(); err == nil || calls != 1 {
		t.Fatalf("normal mode did not restart: calls=%d err=%v", calls, err)
	}
}
