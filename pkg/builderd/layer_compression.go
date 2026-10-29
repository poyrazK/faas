package builderd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// LayerZstdEnv turns on zstd for the layers a source build exports: the OCI
// image imaged converts and the app's dependency cache (ADR-20261028084018).
// BuildKit gzips both by default; on a 2-vCPU builder the export step took
// 147 s of a 276 s cold build. Turn it on for a node's builderd only when that
// node's imaged reads zstd layers; an imaged that does not refuses the
// artifact. It lives here, not in pkg/api, so the switch rebuilds builderd
// alone.
const LayerZstdEnv = "FAAS_BUILD_LAYER_ZSTD"

// ParseLayerZstd parses a LayerZstdEnv value into the BuildManifest
// LayerCompression builderd sends: "" (gzip, the default) or
// api.BuildLayerCompressionZstd. An unparsable value is an error so a typo
// fails startup instead of silently keeping gzip.
func ParseLayerZstd(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	on, err := strconv.ParseBool(raw)
	if err != nil {
		return "", fmt.Errorf("%s must be a boolean (got %q)", LayerZstdEnv, raw)
	}
	if !on {
		return "", nil
	}
	return api.BuildLayerCompressionZstd, nil
}
