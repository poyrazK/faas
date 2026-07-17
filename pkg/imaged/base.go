package imaged

// Base image references per runtime (spec §4.6 two-drive scheme, ADR-005).
//
// imaged.handleDeployment pulls the app's manifest, then pulls the matching
// base's manifest to learn the base's diff_ids, then computes LayersAboveBase.
// The base itself is NOT downloaded — drive0 is the shared read-only ext4
// produced from the base image, already on disk at /srv/fc/base/<runtime>.ext4.
//
// The defaults below match images/runner-node22.Dockerfile,
// images/runner-python312.Dockerfile, and images/base-minimal.Dockerfile on
// HEAD of main. They can be overridden at startup via config (cmd/imaged's
// TOML) so the box can roll a base image ahead of pinned refs and have imaged
// track it without a code change.
const (
	BaseRefNode22    = "ghcr.io/onebox-faas/runner-node22:latest"
	BaseRefPython312 = "ghcr.io/onebox-faas/runner-python312:latest"
	BaseRefMinimal   = "ghcr.io/onebox-faas/base-minimal:latest"
	BaseRefBuilder   = "ghcr.io/onebox-faas/builder-base:latest"

	// Runtime names are the values stored on state.App.Runtime. They map
	// 1:1 to the runner shims in guest/runners/{node22,python312}. Naming
	// them as constants keeps the baseRefFor switch and the production
	// callers (cmd/imaged's deploy path) in lockstep.
	RuntimeNode22    = "node22"
	RuntimePython312 = "python312"
)

// baseRefFor returns the canonical base image reference for a runtime. The
// empty runtime maps to the minimal base (plain apps, spec §4.6).
func baseRefFor(runtime string) string {
	switch runtime {
	case RuntimeNode22:
		return BaseRefNode22
	case RuntimePython312:
		return BaseRefPython312
	default:
		return BaseRefMinimal
	}
}
