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
