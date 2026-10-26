package apidserver

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/state"
)

// Source deploy GA exports (ADR-20261026152731).

var ScanForStatefulShapeAtRoot = scanForStatefulShapeAtRoot

// ResolveSourceDockerfileBuild returns the Dockerfile path and options a
// gregale.yaml build: section selects for the archive at sourcePath.
func ResolveSourceDockerfileBuild(sourcePath, sourceRoot string, app state.App, manifest *gregalemanifest.Manifest) (string, *api.DockerfileBuildOptions, bool, *api.Problem) {
	build, ok, problem := resolveSourceDockerfileBuild(sourcePath, sourceRoot, app, manifest)
	return build.path, build.options, ok, problem
}
