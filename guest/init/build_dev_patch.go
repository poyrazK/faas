package main

import (
	"os"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/devpatch"
)

// railpackPlanPath is where buildArgv asks `railpack prepare` to write the
// plan for this build.
func railpackPlanPath(m api.BuildManifest) string {
	return filepath.Join(filepath.Dir(m.OutDir), "railpack-plan.json")
}

// buildDevPatchSourceMap records whether a successful build copied its source
// into the image unchanged (ADR-740). It reads only the plan Railpack wrote
// inside this builder VM, so the decision never trusts host-side inference.
func buildDevPatchSourceMap(m api.BuildManifest, readFile func(string) ([]byte, error)) *api.DevPatchSourceMap {
	if m.Framework == api.FrameworkDockerfile {
		return &api.DevPatchSourceMap{Version: api.DevPatchSourceMapVersion, Reason: api.DevPatchReasonNotRailpack}
	}
	data, err := readFile(railpackPlanPath(m))
	if err != nil {
		return &api.DevPatchSourceMap{Version: api.DevPatchSourceMapVersion, Reason: api.DevPatchReasonPlanUnreadable}
	}
	sourceMap := devpatch.ClassifyRailpackPlan(data)
	return &sourceMap
}

func readBuildPlan(name string) ([]byte, error) {
	info, err := os.Stat(name)
	if err != nil {
		return nil, err
	}
	// Railpack plans are a few KiB; refuse anything implausibly large rather
	// than copying it into the durable build result.
	if info.Size() > 1<<20 {
		return nil, os.ErrInvalid
	}
	return os.ReadFile(name)
}
