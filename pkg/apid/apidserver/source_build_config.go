package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/state"
)

// sourceDockerfileBuild is a gregale.yaml build: section resolved against the
// uploaded source (ADR-20261026152731): the Dockerfile it selects, relative
// to the source root, and the build arguments, target and secrets.
type sourceDockerfileBuild struct {
	path    string
	options *api.DockerfileBuildOptions
}

// resolveSourceDockerfileBuild returns the Dockerfile build a manifest
// declares, after checking that the selected Dockerfile exists under the
// source root and passes the stateless checks. ok is false without a build:
// section.
func resolveSourceDockerfileBuild(sourcePath, sourceRoot string, app state.App, manifest *gregalemanifest.Manifest) (build sourceDockerfileBuild, ok bool, problem *api.Problem) {
	if manifest == nil || manifest.Build == nil {
		return sourceDockerfileBuild{}, false, nil
	}
	if app.Type == state.AppTypeFunction {
		return sourceDockerfileBuild{}, false, api.NewProblem(http.StatusUnprocessableEntity, CodeAppManifestInvalid,
			"Invalid manifest", "build: applies to app deploys; functions build from the platform runtime")
	}
	path := manifest.Build.DockerfilePath()
	if problem := scanForStatefulShapeWithDockerfileAtRoot(sourcePath, true, sourceRoot, path); problem != nil {
		return sourceDockerfileBuild{}, false, problem
	}
	return sourceDockerfileBuild{path: path, options: manifest.Build.Options()}, true, nil
}
