package main

import (
	"errors"
	"io/fs"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sharedruntime"
)

// buildSharedRuntimeVerdict classifies a successful Node source build of an
// app for the shared runner base (H10-140). Functions already run on it and
// Dockerfiles own their FROM chain, so neither is classified; neither is an
// auto-detected build without a package.json. Like buildDevPatchSourceMap it
// reads only what Railpack wrote inside this builder VM.
func buildSharedRuntimeVerdict(m api.BuildManifest, readFile func(string) ([]byte, error)) *api.SharedRuntimeVerdict {
	if m.Function || (m.Framework != api.FrameworkRailpackNode && m.Framework != api.FrameworkAuto) {
		return nil
	}
	pkg, err := readFile(filepath.Join(m.Workdir, "package.json"))
	if errors.Is(err, fs.ErrNotExist) && m.Framework == api.FrameworkAuto {
		return nil
	}
	if err != nil {
		v := api.SharedRuntimeVerdict{Reason: sharedruntime.ReasonPackageUnreadable}
		return &v
	}
	plan, err := readFile(railpackPlanPath(m))
	if err != nil {
		v := api.SharedRuntimeVerdict{Reason: sharedruntime.ReasonPlanUnreadable}
		return &v
	}
	v := sharedruntime.ClassifyNode(plan, pkg)
	return &v
}
