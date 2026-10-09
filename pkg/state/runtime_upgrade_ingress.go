package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// RuntimeUpgradeIngressBinding is permission for this connection identity at
// one fresh observation, not an ingress fleet proof or a retirement lease.
type RuntimeUpgradeIngressBinding struct {
	GatewayRosterRevision string
	SlotID                string
	SessionID             string
	CheckedAt             time.Time
	ValidForSeconds       int
}

type RuntimeUpgradeIngressBindingStore interface {
	AuthorizeRuntimeUpgradeGatewayIngress(context.Context, string, string) (RuntimeUpgradeIngressBinding, error)
}

func runtimeUpgradeIngressBinding(roster RuntimeUpgradeGatewayRoster, heartbeats []runtimeUpgradeGatewayHeartbeat, slotID, sessionID string, now time.Time) (RuntimeUpgradeIngressBinding, error) {
	out := RuntimeUpgradeIngressBinding{GatewayRosterRevision: roster.Revision, SlotID: slotID, SessionID: sessionID, CheckedAt: now}
	if !canonicalRuntimeUpgradeGatewayUUID(roster.Revision) || len(roster.Members) == 0 || len(roster.Members) > api.RuntimeUpgradeGatewaySessionLimit {
		return out, ErrConflict
	}
	for _, member := range roster.Members {
		if member.SlotID != slotID || member.SessionID != sessionID {
			continue
		}
		for _, h := range heartbeats {
			if h.Revision == roster.Revision && h.SlotID == slotID && h.SessionID == sessionID && !h.SeenAt.After(now) && h.ExpiresAt.After(now) && h.ExpiresAt.Equal(h.SeenAt.Add(api.RuntimeUpgradeGatewayHeartbeatMaxAge)) {
				out.ValidForSeconds = int(h.ExpiresAt.Sub(now) / time.Second)
				if out.ValidForSeconds >= 1 {
					return out, nil
				}
			}
		}
	}
	return out, ErrConflict
}
