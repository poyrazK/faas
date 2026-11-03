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
func ParseDeferCacheExport(raw string) (bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false, nil
	}
	on, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean (got %q)", DeferCacheExportEnv, raw)
	}
	return on, nil
}
