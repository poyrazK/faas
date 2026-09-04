package builderd

import "github.com/onebox-faas/faas/pkg/imaged"

// resolveBuildRuntimeBaseRef chooses the same base reference imaged uses for
// deployment-layer materialisation. The app runtime is authoritative,
// including the empty runtime, which intentionally resolves to the minimal
// base for plain app deployments. Using a framework-derived fallback here
// would make builderd and imaged select different bases for legacy apps.
func resolveBuildRuntimeBaseRef(runtime string, fw Framework, envLookup func(string) string) (string, error) {
	if fw == FrameworkDocker {
		// Dockerfile builds own their FROM chain; injecting a Railpack base
		// into the source would change customer Dockerfile semantics.
		return "", nil
	}
	return imaged.ResolveDeployBaseRef(runtime, envLookup)
}
