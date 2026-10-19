package state

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func qualifyStandardLoggingTx(ctx context.Context, tx pgx.Tx, r ApplicationStandardConsumerRoster) (standardApplicationQualification, error) {
	q := sqlc.New()
	current, err := q.ReadApplicationStandardObservationLogging(ctx, tx, sqlc.ReadApplicationStandardObservationLoggingParams{AppID: mustPgUUID(r.AppID), HeartbeatSeconds: DefaultHeartbeatStaleness.Seconds()})
	if err != nil {
		return standardApplicationQualification{}, err
	}
	var inventory ApplicationStandardLogInventory
	var bindings []ApplicationStandardLogDrainBinding
	if json.Unmarshal(current.Inventory, &inventory) != nil || json.Unmarshal(current.Bindings, &bindings) != nil {
		return standardApplicationQualification{}, ErrApplicationStandardRuntimeStale
	}
	loaded, err := q.ListApplicationStandardLogInventories(ctx, tx, sqlc.ListApplicationStandardLogInventoriesParams{OrgID: mustPgUUID(r.OrgID), AppID: mustPgUUID(r.AppID), FreshnessSeconds: api.ApplicationStandardLogInventoryFreshness.Seconds()})
	if err != nil {
		return standardApplicationQualification{}, err
	}
	inventories, err := decodeStandardObservationRows[ApplicationStandardLogInventoryObservation](loaded)
	if err != nil {
		return standardApplicationQualification{}, err
	}
	reports, err := q.ListApplicationStandardLogHealth(ctx, tx, sqlc.ListApplicationStandardLogHealthParams{OrgID: mustPgUUID(r.OrgID), AppID: mustPgUUID(r.AppID), FreshnessSeconds: api.ApplicationStandardLogHealthFreshness.Seconds()})
	if err != nil {
		return standardApplicationQualification{}, err
	}
	health, err := decodeStandardObservationRows[ApplicationStandardLogHealthObservation](reports)
	if err != nil {
		return standardApplicationQualification{}, err
	}
	result := qualifyStandardLoggingReports(r, inventory, bindings, inventories, health)
	if current.HeartbeatUntil.Valid {
		result.restrict(current.HeartbeatUntil.Time)
	}
	if result.reason != "" {
		return result, nil
	}
	return qualifyStandardEgressTx(ctx, tx, r, result)
}

func qualifyStandardLoggingReports(r ApplicationStandardConsumerRoster, inventory ApplicationStandardLogInventory, bindings []ApplicationStandardLogDrainBinding, inventories []ApplicationStandardLogInventoryObservation, health []ApplicationStandardLogHealthObservation) standardApplicationQualification {
	result := standardApplicationQualification{}
	for _, node := range r.Nodes {
		loaded := standardObservationLoggingNode(node, inventory, inventories, r.ReadAt)
		if loaded.reason != "" {
			return loaded
		}
		if loaded.until.IsZero() {
			continue
		}
		result.restrict(loaded.until)
		for _, binding := range bindings {
			provider := standardObservationProvider(node, binding, health, r.ReadAt)
			if provider.reason != "" {
				return provider
			}
			result.restrict(provider.until)
		}
	}
	return result
}

func qualifyStandardEgressTx(ctx context.Context, tx pgx.Tx, r ApplicationStandardConsumerRoster, result standardApplicationQualification) (standardApplicationQualification, error) {
	raw, err := sqlc.New().ListApplicationStandardEgress(ctx, tx, sqlc.ListApplicationStandardEgressParams{OrgID: mustPgUUID(r.OrgID), AppID: mustPgUUID(r.AppID), FreshnessSeconds: api.ApplicationStandardEgressFreshness.Seconds()})
	if err != nil {
		return result, err
	}
	rows, err := decodeStandardObservationRows[ApplicationStandardEgressObservation](raw)
	if err != nil {
		return result, err
	}
	for _, n := range r.Nodes {
		if !n.NativeRequired {
			continue
		}
		found := false
		for _, row := range rows {
			if row.Target.Identity.NodeID == n.NodeID && row.Target.Identity.Incarnation == n.NativeIncarnation && row.Target.Identity.ProtocolVersion == n.NativeProtocol {
				egress := standardObservationEgress(row.Target, rows, r.ReadAt)
				if egress.reason == "" {
					result.restrict(egress.until)
					found = true
				}
			}
		}
		if !found {
			return standardApplicationQualification{reason: "egress_observation_pending"}, nil
		}
	}
	return result, nil
}

func decodeStandardObservationRows[T any](raw [][]byte) ([]T, error) {
	rows := make([]T, 0, len(raw))
	for _, body := range raw {
		var row T
		if err := json.Unmarshal(body, &row); err != nil {
			return nil, fmt.Errorf("decode application standard observation: %w", err)
		}
		rows = append(rows, row)
	}
	return rows, nil
}
