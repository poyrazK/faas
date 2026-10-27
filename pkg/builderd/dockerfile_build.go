package builderd

import (
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// selectDockerfileBuild picks the Compose build arguments and target for one
// build (ADR-20261019142544): the options captured on the deployment, else the
// app's current ones, the same precedence as the Dockerfile path. A reviewed
// environment source selection replaces the app's build settings and carries
// none. Only Dockerfile builds receive options; ignored reports options that a
// Railpack build drops.
func selectDockerfileBuild(app state.App, dep state.Deployment, reviewedSource bool, fw Framework) (options *api.DockerfileBuildOptions, ignored bool, err error) {
	options = app.Manifest.DockerfileBuild
	if profile, ok := persistedProfile(dep); ok && profile.DockerfileBuild != nil {
		options = profile.DockerfileBuild
	}
	if reviewedSource || options.IsZero() {
		return nil, false, nil
	}
	if fw != FrameworkDocker {
		return nil, true, nil
	}
	if err := options.Validate(); err != nil {
		return nil, false, err
	}
	return options.Clone(), false, nil
}

// shadowedBuildArgs names the build arguments a reviewed public build URL of
// the same name replaces (ADR-1665); guest-init passes the URL instead.
func shadowedBuildArgs(options *api.DockerfileBuildOptions, publicURLs map[string]string) []string {
	var names []string
	for _, name := range options.ArgNames() {
		if _, ok := publicURLs[name]; ok {
			names = append(names, name)
		}
	}
	return slices.Clip(names)
}
