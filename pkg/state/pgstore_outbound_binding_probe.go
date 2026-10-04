package state

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ OutboundBindingProbeStore = (*PgStore)(nil)

func (s *PgStore) GetOutboundBindingProbePolicy(ctx context.Context, accountID, id string) (api.OutboundBindingProbePolicy, error) {
	row, err := sqlc.New().GetOutboundBindingProbePolicy(ctx, s.pool, sqlc.GetOutboundBindingProbePolicyParams{AccountID: mustPgUUID(accountID), IntegrationID: mustPgUUID(id)})
	if err != nil {
		return api.OutboundBindingProbePolicy{}, fmt.Errorf("state: read outbound probe policy: %w", mapErr(err))
	}
	return api.OutboundBindingProbePolicy{Method: row.Method, Path: row.Path, ExpectedStatus: int(row.ExpectedStatus)}, nil
}
func (s *PgStore) SetOutboundBindingProbePolicy(ctx context.Context, accountID, id string, policy *api.OutboundBindingProbePolicy) error {
	if policy == nil {
		// Verify ownership even when an absent policy is deleted idempotently.
		offers, err := s.ListOutboundIntegrationOffers(ctx, accountID)
		if err != nil {
			return err
		}
		owned := false
		for _, offer := range offers {
			owned = owned || offer.ID == id && offer.OwnerKind == "customer"
		}
		if !owned {
			return ErrNotFound
		}
		_, err = sqlc.New().DeleteOutboundBindingProbePolicy(ctx, s.pool, sqlc.DeleteOutboundBindingProbePolicyParams{AccountID: mustPgUUID(accountID), IntegrationID: mustPgUUID(id)})
		if err != nil {
			return fmt.Errorf("state: delete outbound probe policy: %w", mapErr(err))
		}
		return nil
	}
	if !policy.Valid() {
		return ErrInvalidArgument
	}
	_, err := sqlc.New().SetOutboundBindingProbePolicy(ctx, s.pool, sqlc.SetOutboundBindingProbePolicyParams{AccountID: mustPgUUID(accountID), IntegrationID: mustPgUUID(id), Method: policy.Method, Path: policy.Path, ExpectedStatus: int32(policy.ExpectedStatus)})
	if err != nil {
		return fmt.Errorf("state: set outbound probe policy: %w", mapErr(err))
	}
	return nil
}
func (s *PgStore) ListOutboundBindingProbeSnapshots(ctx context.Context, accountID, appID string) ([]OutboundBindingProbeSnapshot, error) {
	rows, err := sqlc.New().ListOutboundBindingProbeSnapshots(ctx, s.pool, sqlc.ListOutboundBindingProbeSnapshotsParams{AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID)})
	if err != nil {
		return nil, fmt.Errorf("state: read outbound probe snapshots: %w", err)
	}
	out := make([]OutboundBindingProbeSnapshot, 0, len(rows))
	for _, row := range rows {
		item := OutboundBindingProbeSnapshot{IntegrationID: uuidString(row.ID)}
		if row.Method.Valid && row.Path.Valid && row.ExpectedStatus.Valid {
			policy := api.OutboundBindingProbePolicy{Method: row.Method.String, Path: row.Path.String, ExpectedStatus: int(row.ExpectedStatus.Int32)}
			item.Policy = &policy
			item.Revision = outboundProbeRevision(row.IntegrationFacts, row.BindingFacts, row.Plan, row.CredentialRevision, policy)
		}
		out = append(out, item)
	}
	return out, nil
}
