package state

import (
	"encoding/json"
	"fmt"
)

func scanDevSyncHistory(row rowScanner) (DevSyncHistory, error) {
	var out DevSyncHistory
	var phases []byte
	if err := row.Scan(
		&out.ID, &out.AppID, &out.DeploymentID, &out.Status,
		&out.EditToLiveMS, &out.SLOTargetMS, &out.WithinSLO, &phases, &out.CreatedAt,
	); err != nil {
		return DevSyncHistory{}, err
	}
	if !json.Valid(phases) {
		return DevSyncHistory{}, fmt.Errorf("state: stored developer sync phases are invalid")
	}
	out.Phases = append(json.RawMessage(nil), phases...)
	return out, nil
}
