package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ RuntimeAppEnvStore = (*PgStore)(nil)

func (s *PgStore) RuntimeAppEnvForDeployment(ctx context.Context, accountID, appID, deploymentID string) (RuntimeAppEnvSnapshot, error) {
	if err := validateRuntimeAppEnvIDs(accountID, appID, deploymentID); err != nil {
		return RuntimeAppEnvSnapshot{}, err
	}
	row, err := sqlc.New().ReadRuntimeAppEnvForDeployment(ctx, s.pool, sqlc.ReadRuntimeAppEnvForDeploymentParams{
		AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), DeploymentID: mustPgUUID(deploymentID),
	})
	if err != nil {
		return RuntimeAppEnvSnapshot{}, mapErr(err)
	}
	return decodeRuntimeAppEnvSnapshot(accountID, appID, deploymentID, row.Scope, row.EnvironmentID, row.Values)
}

func decodeRuntimeAppEnvSnapshot(accountID, appID, deploymentID, scope, environmentID string, raw []byte) (RuntimeAppEnvSnapshot, error) {
	var values []struct {
		Key       string    `json:"key"`
		Value     string    `json:"value"`
		CreatedAt time.Time `json:"created_at"`
		UpdatedAt time.Time `json:"updated_at"`
	}
	if err := json.Unmarshal(raw, &values); err != nil {
		return RuntimeAppEnvSnapshot{}, ErrConflict
	}
	result := RuntimeAppEnvSnapshot{AccountID: accountID, AppID: appID, DeploymentID: deploymentID,
		Scope: scope, EnvironmentID: environmentID, Values: make([]AppEnv, 0, len(values))}
	for _, value := range values {
		result.Values = append(result.Values, AppEnv{AccountID: accountID, AppID: appID, Scope: result.Scope,
			Key: value.Key, Value: value.Value, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt})
	}
	return result, nil
}
