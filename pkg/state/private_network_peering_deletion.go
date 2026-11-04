package state

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// DeletePrivateNetworkPeeringDurably rolls back the deletion if its only
// durable revocation cue cannot be stored. Outbox retries survive the last
// peering row disappearing and do not depend on a future periodic discovery.
func (s *PgStore) deletePrivateNetworkPeeringDurably(ctx context.Context, accountID, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	id = strings.TrimSpace(id)
	row, err := sqlc.New().DeletePrivateNetworkPeeringForRevocation(ctx, tx, sqlc.DeletePrivateNetworkPeeringForRevocationParams{AccountID: mustPgUUID(accountID), ID: id})
	if err != nil {
		return mapErr(err)
	}
	payload, err := json.Marshal(struct {
		Kind           string `json:"kind"`
		AccountID      string `json:"account_id"`
		PeeringID      string `json:"peering_id"`
		Region         string `json:"region"`
		LeftNetworkID  string `json:"left_network_id"`
		RightNetworkID string `json:"right_network_id"`
		Status         string `json:"status"`
	}{"private_network_peering", accountID, id, row.Region, row.LeftNetworkID, row.RightNetworkID, "deleted"})
	if err != nil {
		return err
	}
	if err := db.EnqueueDurableNotificationTx(ctx, tx, db.NotifyPrivateNetworkChanged, string(payload)); err != nil {
		return err
	}
	return mapErr(tx.Commit(ctx))
}
