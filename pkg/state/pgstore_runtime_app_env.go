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
	snapshot, err := decodeRuntimeAppEnvSnapshot(accountID, appID, deploymentID, row.Scope, row.EnvironmentID, row.Values)
	if err != nil {
		return RuntimeAppEnvSnapshot{}, err
	}
	if snapshot.Inherited, err = decodeInheritedProjectEnv(snapshot, row.ProjectConfig); err != nil {
		return RuntimeAppEnvSnapshot{}, err
	}
	// A PR preview inherits no project environment; its production app's
	// preview values take that place (ADR-20261028100000).
	preview, err := decodePreviewInheritedEnv(snapshot, row.PreviewValues)
	if err != nil {
		return RuntimeAppEnvSnapshot{}, err
	}
	if len(preview) > 0 {
		snapshot.Inherited = preview
	}
	return snapshot, nil
}

// decodeInheritedProjectEnv reads the project_config column of the runtime
// snapshot queries (ADR-1136): the latest configuration version of the
// project environment the deployment's scope maps to, or an empty object.
func decodeInheritedProjectEnv(snapshot RuntimeAppEnvSnapshot, raw []byte) ([]AppEnv, error) {
	var project struct {
		Values    json.RawMessage `json:"values"`
		CreatedAt time.Time       `json:"created_at"`
	}
	if err := json.Unmarshal(raw, &project); err != nil {
		return nil, ErrConflict
	}
	return inheritedProjectEnv(snapshot.AccountID, snapshot.AppID, snapshot.Scope, snapshot.Values, project.Values, project.CreatedAt.UTC())
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
