// adr: 570
package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type GatewayPolicyKind string

const (
	GatewayPolicyControlPlane GatewayPolicyKind = "control_plane"
	GatewayPolicyEdgeRules    GatewayPolicyKind = "edge_rules"
	GatewayPolicyCorsPresets  GatewayPolicyKind = "cors_presets"
	GatewayPolicyCachePurge   GatewayPolicyKind = "cache_purge"
)

// ReportGatewayPolicyProgress publishes only after successful local replay.
// Ownership is checked and locked in the same SQL statement as the write;
// replacement cannot race an old process's acknowledgement into the new epoch.
func (s *PgStore) ReportGatewayPolicyProgress(ctx context.Context, epoch GatewayTrafficEpoch, kind GatewayPolicyKind, lastChangeID int64) error {
	if s == nil || s.pool == nil {
		return errors.New("gateway policy progress has nil pool")
	}
	id, err := parsePgUUID(epoch.BootID)
	if err != nil || epoch.Generation <= 0 || epoch.NodeName == "" || lastChangeID < 0 {
		return errors.New("invalid gateway policy progress")
	}
	switch kind {
	case GatewayPolicyControlPlane, GatewayPolicyEdgeRules, GatewayPolicyCorsPresets, GatewayPolicyCachePurge:
	default:
		return errors.New("invalid gateway policy component")
	}
	n, err := sqlc.New().ReportGatewayPolicyProgress(ctx, s.pool, sqlc.ReportGatewayPolicyProgressParams{
		NodeName: epoch.NodeName, Generation: epoch.Generation, BootID: id, PolicyKind: string(kind), LastChangeID: lastChangeID,
	})
	if err != nil {
		return fmt.Errorf("report gateway policy progress: %w", err)
	}
	if n != 1 {
		return ErrGatewayTrafficEpochLost
	}
	return nil
}
