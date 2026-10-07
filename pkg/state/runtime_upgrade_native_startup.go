package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway/edgetopology"
	"github.com/onebox-faas/faas/pkg/gateway/ingress"
)

type RuntimeUpgradeNativePublicStartupReview struct {
	GatewayRevision, PublicRevision string
	Selection                       edgetopology.NativeStartupReview
}

// Historical selected provenance only. Reading this record does not establish
// current liveness, hardware attribution, complete scope or a fencing receipt.
type RuntimeUpgradeNativePublicStartup struct {
	GatewayRevision, PublicRevision string
	Startup                         ingress.NativePublicStartup
	ReviewSHA256, EnvelopeSHA256    string
	Review, Envelope                []byte
	ObservedAt, RecordedAt          time.Time
}

// Private administrative seam. First enrollment runs the concrete retained
// native collector, never a caller-supplied boolean/DTO verifier (ADR-625).
type RuntimeUpgradeNativePublicStartupStore interface {
	RecordRuntimeUpgradeNativePublicStartup(context.Context, RuntimeUpgradeNativePublicStartupReview, *edgetopology.NativeStartupProbe) (RuntimeUpgradeNativePublicStartup, error)
	RuntimeUpgradeNativePublicStartup(context.Context, string, string) (RuntimeUpgradeNativePublicStartup, error)
}
