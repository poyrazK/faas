package builderd

import (
	"fmt"
	"strconv"
	"strings"
)

// DeferCacheExportEnv moves a build's dependency-cache export after the image
// handoff (ADR-20261029171525). The builder VM exports only the image, and
// builderd hands it to imaged. A second VM on the same drive then exports the
// cache while imaged scans and snapshots. Default off.
const DeferCacheExportEnv = "FAAS_BUILD_DEFER_CACHE_EXPORT"

// ParseDeferCacheExport parses a DeferCacheExportEnv value. An unparsable
// value is an error so a typo fails startup.
//
// Turning the switch on is refused for now (bug hunt H104): the second VM's
// drive is prepared by replaying its journal with e2fsck and rewriting files
// with debugfs on the host, unsandboxed, as faas-builderd
// (prepareCacheExportDrive). The drive is the build's, so any account with a
// build would hand a crafted ext4 to the host's libext2fs. The switch can come
// back once the cache-export inputs reach the guest without a host-side edit
// (ADR-20261025090100: the host never parses a guest-written drive).
func ParseDeferCacheExport(raw string) (bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false, nil
	}
	on, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean (got %q)", DeferCacheExportEnv, raw)
	}
	if on {
		return false, fmt.Errorf("%s cannot be enabled: the cache export run edits the build's drive on the host (bug hunt H104, ADR-20261025090100)", DeferCacheExportEnv)
	}
	return false, nil
}
