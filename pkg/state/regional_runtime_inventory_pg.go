package state

// adr: 635
import (
	"context"
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) ListRegionalRuntimeInventory(ctx context.Context, cell string, after RegionalRuntimeRetirementCursor, limit int) ([]RegionalRuntimeInventoryEntry, error) {
	if !validRegionalInventoryPage(cell, after, limit) {
		return nil, ErrInvalidArgument
	}
	rows, err := (&sqlc.Queries{}).ListRegionalRuntimeInventory(ctx, s.pool, sqlc.ListRegionalRuntimeInventoryParams{CellID: cell, FirstPage: after.AppID == "", AfterApp: uuidToPgtype(after.AppID), AfterPermit: uuidToPgtype(after.PermitID), BatchLimit: int32(limit)})
	if err != nil {
		return nil, regionalCapacityError(err)
	}
	result := make([]RegionalRuntimeInventoryEntry, 0, len(rows))
	for _, row := range rows {
		entry, err := regionalInventoryRow(row, cell)
		if err != nil {
			return nil, err
		}
		result = append(result, entry)
	}
	return result, nil
}

func regionalInventoryRow(row sqlc.ListRegionalRuntimeInventoryRow, cell string) (RegionalRuntimeInventoryEntry, error) {
	receipt, err := regionalCapacityRow(sqlc.RegionalRuntimeCapacityReceipt{
		AppID: row.AppID, AllocationID: row.AllocationID, PermitID: row.PermitID, Generation: row.Generation,
		InstanceID: row.InstanceID, RunID: row.RunID, DeploymentID: row.DeploymentID, NodeID: row.NodeID,
		HostEpochID: row.HostEpochID, Kind: row.Kind, PermitClaims: row.PermitClaims,
		CreatedAt: row.CreatedAt, IssuedAt: row.IssuedAt, FenceID: row.FenceID,
		ReleasedAt: row.ReleasedAt, StopClaims: row.StopClaims, StopEnvelope: row.StopEnvelope,
	})
	if err != nil {
		return RegionalRuntimeInventoryEntry{}, err
	}
	if (cell != "" && receipt.Permit.Grant.Binding.CellID != cell) || receipt.IssuedAt == nil || receipt.ReleasedAt != nil || row.ServingSeatsHeld < 0 {
		return RegionalRuntimeInventoryEntry{}, ErrConflict
	}
	entry := RegionalRuntimeInventoryEntry{Receipt: receipt, ServingSeatsHeld: row.ServingSeatsHeld}
	if row.SourceInstanceKey.Valid {
		source := runtimequota.LaunchSource{InstanceKey: row.SourceInstanceKey.String, TaskID: exclusiveUUID(row.SourceTaskID)}
		if err := json.Unmarshal(row.SourcePermitClaims, &source.Permit); err != nil || source.Validate() != nil || source.Permit != receipt.Permit {
			return RegionalRuntimeInventoryEntry{}, ErrConflict
		}
		entry.Source = &source
	}
	if row.ResourceClaims != nil {
		if entry.Source == nil || json.Unmarshal(row.ResourceClaims, &entry.Resources) != nil || !entry.Resources.Valid() {
			return RegionalRuntimeInventoryEntry{}, ErrConflict
		}
	}
	return entry, nil
}
