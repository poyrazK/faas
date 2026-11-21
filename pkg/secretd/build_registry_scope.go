package secretd

import (
	"fmt"
	"slices"

	"google.golang.org/grpc/codes"

	"github.com/onebox-faas/faas/pkg/dockerfilebase"
	"github.com/onebox-faas/faas/pkg/logsanitize"
)

// BuildRegistryScopeMode is how secretd holds a build's registry pulls to
// the repositories its deployment's Dockerfiles name (ADR-20261110210000
// slice 3j-4, deployments.build_registry_scope).
type BuildRegistryScopeMode string

const (
	// BuildRegistryScopeOff checks nothing: a running build's node pulls
	// anything its app's credential reaches (slice 3j-3).
	BuildRegistryScopeOff BuildRegistryScopeMode = "off"
	// BuildRegistryScopeObserve counts each manifest pull as in scope, out
	// of scope or unrecorded and logs the last two, refusing nothing.
	BuildRegistryScopeObserve BuildRegistryScopeMode = "observe"
	// BuildRegistryScopeEnforce refuses a pull outside the recorded scope,
	// and every pull of a build whose deployment recorded none.
	BuildRegistryScopeEnforce BuildRegistryScopeMode = "enforce"
)

// ParseBuildRegistryScopeMode reads FAAS_SECRETD_BUILD_REGISTRY_SCOPE; empty
// is observe.
func ParseBuildRegistryScopeMode(v string) (BuildRegistryScopeMode, error) {
	switch m := BuildRegistryScopeMode(v); m {
	case "":
		return BuildRegistryScopeObserve, nil
	case BuildRegistryScopeOff, BuildRegistryScopeObserve, BuildRegistryScopeEnforce:
		return m, nil
	}
	return "", fmt.Errorf("secretd: build registry scope %q: want off, observe or enforce", v)
}

// WithBuildRegistryScope sets how builds' registry pulls are held to their
// deployment's recorded scope (slice 3j-4). The default is observe.
func (s *Server) WithBuildRegistryScope(mode BuildRegistryScopeMode) *Server {
	s.buildScope = mode
	return s
}

// checkBuildRegistryScope holds a build's pull to its deployment's recorded
// scope. The registry's /v2/ ping names no repository and passes.
func (s *Server) checkBuildRegistryScope(buildID, node string, run BuildRun, target registryTarget) error {
	if s.buildScope == BuildRegistryScopeOff || target.url.Path == "/v2/" {
		return nil
	}
	result := scopeUnrecorded
	if run.ScopeRecorded {
		result = scopeOutOfScope
		if key, err := dockerfilebase.RepositoryKey(target.url.Host, target.repository); err == nil && slices.Contains(run.Scope, key) {
			result = scopeInScope
		}
	}
	// A pull reads one manifest and many blobs: count and log the manifest.
	if target.manifest {
		s.metrics.scope(result)
		if result != scopeInScope {
			s.log.Warn("secretd: build registry pull outside its deployment's scope", "result", result,
				"mode", string(s.buildScope), "node", logsanitize.Field(node), "build_id", logsanitize.Field(buildID),
				"registry", logsanitize.Field(target.url.Host), "repository", logsanitize.Field(target.repository))
		}
	}
	if result == scopeInScope || s.buildScope != BuildRegistryScopeEnforce {
		return nil
	}
	s.metrics.refuse(rpcRegistryFetch, ReasonOutOfScope)
	if result == scopeUnrecorded {
		return rpcErr(codes.PermissionDenied, "secretd refused: the build's deployment recorded no registry scope")
	}
	return rpcErr(codes.PermissionDenied, "secretd refused: the build's Dockerfiles do not name that repository")
}
