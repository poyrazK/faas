package builderd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// KernelOverlayEnv controls whether builder VMs run BuildKit on kernel overlayfs
// instead of fuse-overlayfs (ADR-20261029105325). Rootless BuildKit then picks
// the snapshotter itself and falls back to fuse-overlayfs when the guest kernel
// refuses an unprivileged overlay mount. On by default since the native
// measurement (a cold Node build 351 s → 116 s); false turns it off.
const KernelOverlayEnv = "FAAS_BUILD_KERNEL_OVERLAY"

// ParseKernelOverlay parses a KernelOverlayEnv value into the BuildManifest
// BuildKitSnapshotter builderd sends: api.BuildKitSnapshotterAuto, or ""
// (fuse-overlayfs) when the value is false. An unparsable value is an error so
// a typo fails startup.
func ParseKernelOverlay(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return api.BuildKitSnapshotterAuto, nil
	}
	on, err := strconv.ParseBool(raw)
	if err != nil {
		return "", fmt.Errorf("%s must be a boolean (got %q)", KernelOverlayEnv, raw)
	}
	if !on {
		return "", nil
	}
	return api.BuildKitSnapshotterAuto, nil
}
