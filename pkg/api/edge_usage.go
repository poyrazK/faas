package api

import (
	"crypto/md5" //nolint:gosec // a stable identifier, not a security boundary
	"encoding/hex"
)

// Edge and realtime usage IDs (ADR-20261104132753). Bytes Gregale sends for an
// app without an instance (response-cache hits, fixed edge responses, log
// drain deliveries, realtime fan-out) are metered in usage_minutes under a
// per-app ID in place of an instances row, so they count toward the app's
// monthly egress, its ceiling and its overage like the instance's own bytes.
// Postgres derives the same IDs as md5('<prefix>' || app_id)::uuid.
const (
	EdgeUsageIDPrefix     = "gregale-edge-egress:"
	RealtimeUsageIDPrefix = "gregale-realtime-egress:"
)

// EdgeUsageInstanceID is the usage_minutes instance_id of appID's edge bytes.
func EdgeUsageInstanceID(appID string) string { return usageID(EdgeUsageIDPrefix + appID) }

// RealtimeUsageInstanceID is the usage_minutes instance_id of appID's realtime
// delivered bytes.
func RealtimeUsageInstanceID(appID string) string { return usageID(RealtimeUsageIDPrefix + appID) }

func usageID(seed string) string {
	sum := md5.Sum([]byte(seed)) //nolint:gosec // see import
	h := hex.EncodeToString(sum[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}
