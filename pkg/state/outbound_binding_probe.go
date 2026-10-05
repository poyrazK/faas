// adr: 430 — private catalog revisions pin credentials without exposing them.
package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/api"
)

type OutboundBindingProbeSnapshot struct {
	IntegrationID string
	Policy        *api.OutboundBindingProbePolicy
	Revision      string
}

type OutboundBindingProbeStore interface {
	GetOutboundBindingProbePolicy(context.Context, string, string) (api.OutboundBindingProbePolicy, error)
	SetOutboundBindingProbePolicy(context.Context, string, string, *api.OutboundBindingProbePolicy) error
	ListOutboundBindingProbeSnapshots(context.Context, string, string) ([]OutboundBindingProbeSnapshot, error)
}

func outboundProbeRevision(facts ...any) string {
	raw, _ := json.Marshal(facts)
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}
