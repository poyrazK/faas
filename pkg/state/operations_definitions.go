package state

import (
	"context"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (m *MemStore) OperationDefinitionForRoute(_ context.Context, accountID, appID, deploymentID, method, path string) (OperationDefinition, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, def := range m.operationMemoryLocked().definitions {
		if def.AccountID == accountID && def.AppID == appID && def.DeploymentID == deploymentID && def.Spec.Method == method && def.Spec.Path == path {
			return cloneOperationDefinition(def), nil
		}
	}
	return OperationDefinition{}, ErrNotFound
}

func (s *PgStore) OperationDefinitionForRoute(ctx context.Context, accountID, appID, deploymentID, method, path string) (OperationDefinition, error) {
	account, err := operationUUID(accountID)
	if err != nil {
		return OperationDefinition{}, err
	}
	app, err := operationUUID(appID)
	if err != nil {
		return OperationDefinition{}, err
	}
	dep, err := operationUUID(deploymentID)
	if err != nil {
		return OperationDefinition{}, err
	}
	row, err := sqlc.New().GetCustomerOperationDefinitionForRoute(ctx, s.pool, sqlc.GetCustomerOperationDefinitionForRouteParams{AccountID: account, AppID: app, DeploymentID: dep, Method: method, Path: path})
	if err != nil {
		return OperationDefinition{}, mapErr(err)
	}
	return operationPGDefinition(sqlc.GetCustomerOperationDefinitionRow(row))
}

func (m *MemStore) OperationDefinitionsForDeployment(_ context.Context, accountID, appID, deploymentID string) ([]OperationDefinition, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []OperationDefinition{}
	for _, def := range m.operationMemoryLocked().definitions {
		if def.AccountID == accountID && def.AppID == appID && def.DeploymentID == deploymentID {
			out = append(out, cloneOperationDefinition(def))
		}
	}
	return out, nil
}

func (s *PgStore) OperationDefinitionsForDeployment(ctx context.Context, accountID, appID, deploymentID string) ([]OperationDefinition, error) {
	account, err := operationUUID(accountID)
	if err != nil {
		return nil, err
	}
	app, err := operationUUID(appID)
	if err != nil {
		return nil, err
	}
	dep, err := operationUUID(deploymentID)
	if err != nil {
		return nil, err
	}
	rows, err := sqlc.New().ListCustomerOperationDefinitionsForDeployment(ctx, s.pool, sqlc.ListCustomerOperationDefinitionsForDeploymentParams{AccountID: account, AppID: app, DeploymentID: dep})
	if err != nil {
		return nil, err
	}
	out := make([]OperationDefinition, 0, len(rows))
	for _, row := range rows {
		def, err := operationPGDefinition(sqlc.GetCustomerOperationDefinitionRow(row))
		if err != nil {
			return nil, err
		}
		out = append(out, def)
	}
	return out, nil
}
