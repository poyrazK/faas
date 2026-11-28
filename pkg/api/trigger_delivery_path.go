package api

import (
	"path"
	"strings"
)

// TriggerBatchSizeHeader is set on a batched trigger delivery (ADR-1321); its
// value is the record count. The SDKs recognize a batch by it, so only the
// platform may set it: the gateway drops it from every request it did not
// deliver itself.
const TriggerBatchSizeHeader = "X-Gregale-Batch-Size"

// TriggerDeliveryPathPrefix is the path the gateway delivers trigger records
// to (POST /_triggers/{source}/{trigger_id}). It is reserved: a request the
// platform did not deliver is refused there, so a public client cannot post
// records that look like a broker delivery (prod hunt r80, 2026-11-28).
const TriggerDeliveryPathPrefix = "/_triggers/"

// IsTriggerDeliveryPath reports whether p addresses the reserved trigger
// delivery path. It ignores case, repeated slashes and dot-segments, because
// app routers differ in how they normalize them.
func IsTriggerDeliveryPath(p string) bool {
	if p == "" {
		return false
	}
	cleaned := strings.ToLower(path.Clean("/" + p))
	reserved := strings.TrimSuffix(TriggerDeliveryPathPrefix, "/")
	return cleaned == reserved || strings.HasPrefix(cleaned, TriggerDeliveryPathPrefix)
}
