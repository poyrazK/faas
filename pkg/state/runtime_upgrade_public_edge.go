package state

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type RuntimeUpgradePublicEdgeMember struct {
	SlotID       string `json:"slot_id"`
	SessionID    string `json:"session_id"`
	ConfigSHA256 string `json:"config_sha256"`
}

// The topology digest names external DNS/Caddy/inventory evidence reviewed by
// the platform administrator. A digest alone is not native coverage proof.
type RuntimeUpgradePublicEdgeRoster struct {
	Revision              string                           `json:"revision"`
	GatewayRosterRevision string                           `json:"gateway_roster_revision"`
	TopologySHA256        string                           `json:"topology_sha256"`
	Members               []RuntimeUpgradePublicEdgeMember `json:"members"`
	CreatedAt             time.Time                        `json:"created_at"`
}

type RuntimeUpgradePublicEdgeObservation struct {
	Status, Reason, Revision, GatewayRosterRevision, TopologySHA256 string
	ExpectedEdges, ConfirmedEdges, ValidForSeconds                  int
	CheckedAt                                                       time.Time
}

// These PostgreSQL-only interfaces grant no retirement or customer authority.
type RuntimeUpgradePublicEdgeRosterStore interface {
	ReviewRuntimeUpgradePublicEdgeRoster(context.Context, string, string, string, []RuntimeUpgradePublicEdgeMember) (RuntimeUpgradePublicEdgeRoster, error)
	RuntimeUpgradePublicEdgeRoster(context.Context) (RuntimeUpgradePublicEdgeRoster, error)
	ObserveRuntimeUpgradePublicEdges(context.Context, string) (RuntimeUpgradePublicEdgeObservation, error)
}

type RuntimeUpgradePublicEdgeGuardStore interface {
	RecordRuntimeUpgradePublicEdgeGuard(context.Context, RuntimeUpgradePublicEdgeMember) error
}

func canonicalPublicEdgeDigest(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == sha256.Size && hex.EncodeToString(b) == s
}

func validPublicEdgeMember(m RuntimeUpgradePublicEdgeMember) bool {
	return canonicalRuntimeUpgradeGatewayUUID(m.SlotID) && canonicalRuntimeUpgradeGatewayUUID(m.SessionID) && canonicalPublicEdgeDigest(m.ConfigSHA256)
}

func canonicalPublicEdgeMembers(expected, gateway, topology string, members []RuntimeUpgradePublicEdgeMember) ([]RuntimeUpgradePublicEdgeMember, error) {
	if (expected != "" && !canonicalRuntimeUpgradeGatewayUUID(expected)) || !canonicalRuntimeUpgradeGatewayUUID(gateway) || !canonicalPublicEdgeDigest(topology) || len(members) < 1 || len(members) > api.RuntimeUpgradePublicEdgeLimit {
		return nil, ErrInvalidArgument
	}
	out := slices.Clone(members)
	sessions := make(map[string]bool, len(out))
	for _, m := range out {
		if !validPublicEdgeMember(m) || sessions[m.SessionID] {
			return nil, ErrInvalidArgument
		}
		sessions[m.SessionID] = true
	}
	slices.SortFunc(out, func(a, b RuntimeUpgradePublicEdgeMember) int { return cmp.Compare(a.SlotID, b.SlotID) })
	for i := 1; i < len(out); i++ {
		if out[i-1].SlotID == out[i].SlotID {
			return nil, ErrInvalidArgument
		}
	}
	return out, nil
}
