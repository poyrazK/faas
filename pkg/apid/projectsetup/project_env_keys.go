package projectsetup

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ProjectEnvKeyReader reads the values a project environment shares with
// every service: its sealed secrets (ADR-1139) and its configuration.
type ProjectEnvKeyReader interface {
	ListProjectEnvironmentSecrets(ctx context.Context, accountID, projectID, environmentSlug string) ([]state.ProjectEnvironmentSecret, error)
	ProjectEnvironmentConfigLatest(ctx context.Context, accountID, projectID, environmentSlug string) (state.ProjectEnvironmentConfig, error)
}

// ProjectEnvironmentKeys returns the key names a project environment gives
// every one of its services, so a plan does not ask for a value that
// `gregale environment secrets set` already supplied. A deploy plan reported
// "Needs a value: JWT_PUBLIC_KEY" for a key set that way (boa multi-service
// deploy, 2026-11-21). Only names are read; no value enters the result. An
// empty project ID (a project the deploy creates) has no shared keys.
func ProjectEnvironmentKeys(ctx context.Context, store ProjectEnvKeyReader, accountID, projectID, scope string) (map[string]bool, error) {
	keys := map[string]bool{}
	if projectID == "" {
		return keys, nil
	}
	environment := scope
	if state.IsProductionScope(environment) {
		environment = "production"
	}
	secrets, err := store.ListProjectEnvironmentSecrets(ctx, accountID, projectID, environment)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return nil, fmt.Errorf("list project environment secrets: %w", err)
	}
	for _, secret := range secrets {
		keys[secret.Key] = true
	}
	config, err := store.ProjectEnvironmentConfigLatest(ctx, accountID, projectID, environment)
	if errors.Is(err, state.ErrNotFound) {
		return keys, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read project environment configuration: %w", err)
	}
	variables, err := api.ProjectEnvironmentConfigEnv(config.Values)
	if err != nil {
		return nil, fmt.Errorf("read project environment configuration: %w", err)
	}
	for key := range variables {
		keys[key] = true
	}
	return keys, nil
}

// WithoutKeys drops the keys in provided from unset, keeping its order.
func WithoutKeys(unset []string, provided map[string]bool) []string {
	if len(provided) == 0 {
		return unset
	}
	out := make([]string, 0, len(unset))
	for _, key := range unset {
		if !provided[key] {
			out = append(out, key)
		}
	}
	return out
}
