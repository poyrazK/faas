package api

import (
	"fmt"
	"strconv"
	"strings"
)

// BuildLayerZstdEnv turns on zstd for the layers a source build exports: the
// OCI image imaged converts and the app's dependency cache (ADR-20261028084018).
// BuildKit gzips both by default; on a 2-vCPU builder the export step took
// 147 s of a 276 s cold build. Turn it on for a node's builderd only when that
// node's imaged reads zstd layers; an imaged that does not refuses the
// artifact.
const BuildLayerZstdEnv = "FAAS_BUILD_LAYER_ZSTD"

// BuildLayerCompressionZstd is the only BuildManifest.LayerCompression value.
const BuildLayerCompressionZstd = "zstd"

// ParseBuildLayerZstd parses a BuildLayerZstdEnv value into the
// BuildManifest.LayerCompression builderd sends: "" (gzip, the default) or
// BuildLayerCompressionZstd. An unparsable value is an error so a typo fails
// startup instead of silently keeping gzip.
func ParseBuildLayerZstd(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	on, err := strconv.ParseBool(raw)
	if err != nil {
		return "", fmt.Errorf("%s must be a boolean (got %q)", BuildLayerZstdEnv, raw)
	}
	if !on {
		return "", nil
	}
	return BuildLayerCompressionZstd, nil
}
