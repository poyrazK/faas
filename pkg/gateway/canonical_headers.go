package gateway

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/wire"
)

// Canonical spellings of pkg/wire header names that are lowercase there (they
// double as gRPC metadata keys, which must be lowercase). http.Header Get/Set/
// Del canonicalize their key anyway, allocating a canonical copy of a
// lowercase name on every call; the hot path passes these instead.
var (
	canonicalClientIPHeader = http.CanonicalHeaderKey(wire.ClientIPHeader)
	canonicalWakeHeader     = http.CanonicalHeaderKey(wire.WakeHeader)
)
