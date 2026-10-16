package state

import (
	"context"

	"github.com/google/uuid"
)

// RuntimeAppEnvSnapshot is read atomically for a host-authenticated instance's
// deployment. The guest supplies neither a stage selector nor ownership IDs.
type RuntimeAppEnvSnapshot struct {
	AccountID, AppID, DeploymentID, Scope, EnvironmentID string
	Values                                               []AppEnv
	// Inherited holds the project environment configuration variables the
	// deployment inherits (ADR-1136), sorted by key, without the keys the app
	// sets itself in the same scope. They are delivered below the app's own
	// values and are not part of runtime receipts.
	Inherited []AppEnv
}

type RuntimeAppEnvStore interface {
	RuntimeAppEnvForDeployment(context.Context, string, string, string) (RuntimeAppEnvSnapshot, error)
}

func validateRuntimeAppEnvIDs(accountID, appID, deploymentID string) error {
	for _, id := range []string{accountID, appID, deploymentID} {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed == uuid.Nil {
			return ErrInvalidArgument
		}
	}
	return nil
}
