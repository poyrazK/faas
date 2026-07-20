package builderd

import "github.com/onebox-faas/faas/pkg/api"

// MapFramework translates the host-side autodetected Framework (see
// detect.go: node/python/docker/unknown) into the api.BuildFramework that
// guest-init branches on in the in-VM dispatcher (guest/init/main_linux.go).
//
// The two enums deliberately carry different vocabulary so the host side
// can name "what we saw in the tarball" (a Dockerfile) while the guest side
// names "what to run" (buildctl --frontend dockerfile). Keeping the mapping
// here means a future language profile (e.g. Go, Rust) only adds a row to
// this table — neither the detector nor guest-init changes.
//
// Issue #54: the previous code cast builderd.Framework straight into
// api.BuildFramework, which produced "docker" where guest-init expected
// "dockerfile", causing every `faas deploy --dockerfile` to fall through
// to Railpack --plan auto and fail on any non-Railpack Dockerfile. This
// function is the fix.
func MapFramework(fw Framework) api.BuildFramework {
	switch fw {
	case FrameworkNode:
		return api.FrameworkRailpackNode
	case FrameworkPython:
		return api.FrameworkRailpackPython
	case FrameworkDocker:
		return api.FrameworkDockerfile
	case FrameworkUnknown:
		// Let guest-init try Railpack auto-detection rather than fail at the
		// dispatcher switch; this matches the previous behaviour for the
		// tarballs the detector doesn't recognise and keeps the failure mode
		// inside the VM (user-visible railpack error) instead of an
		// orchestrator-level reject.
		return api.FrameworkAuto
	default:
		return api.FrameworkAuto
	}
}
