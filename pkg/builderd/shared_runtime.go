package builderd

import "github.com/onebox-faas/faas/pkg/api"

// sharedRuntimeBuildLog reports guest-init's shared-runtime classification
// (H10-140) so operators can see how many Node app builds would leave their
// toolchain out of the app layer before imaged acts on it.
func sharedRuntimeBuildLog(v *api.SharedRuntimeVerdict) string {
	switch {
	case v == nil:
		return ""
	case v.Eligible:
		return "shared runtime: eligible (the app layer would need only /app)\n"
	default:
		return "shared runtime: not eligible (" + v.Reason + ")\n"
	}
}
