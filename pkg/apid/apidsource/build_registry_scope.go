package apidsource

import (
	"github.com/onebox-faas/faas/pkg/frameworkprofile"
)

// sourceBuildRegistryScope (ADR-20261110210000 slice 3j-4) lists the image
// repositories the accepted source's Dockerfiles name, for
// deployments.build_registry_scope: secretd lets the deployment's builds
// pull only those with the app's registry credential. A source without a
// Dockerfile records an empty scope (no private pull is expected); one that
// cannot be read records none, which secretd treats as unscoped.
func sourceBuildRegistryScope(p EnqueueParams) []string {
	if p.SourcePath == "" {
		return nil
	}
	var explicit []string
	if p.DockerfilePath != "" {
		explicit = []string{p.DockerfilePath}
	}
	var args map[string]string
	if !p.DockerfileBuild.IsZero() {
		args = p.DockerfileBuild.Args
	}
	result, found, err := frameworkprofile.DockerfileRegistryScope(p.SourcePath, p.SourceRoot, explicit, args)
	if err != nil {
		if p.Log != nil {
			p.Log.Warn("apidsource.Enqueue: build registry scope not recorded", "app", p.AppID, "err", err)
		}
		return nil
	}
	if !found {
		return []string{}
	}
	if result.Unresolved > 0 && p.Log != nil {
		p.Log.Info("apidsource.Enqueue: Dockerfile image references left out of the build registry scope",
			"app", p.AppID, "unresolved", result.Unresolved)
	}
	if result.Repositories == nil {
		return []string{}
	}
	return result.Repositories
}
