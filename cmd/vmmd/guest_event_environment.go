package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

// guestEventEnvironment is the environment an in-guest publish belongs to:
// always the publishing deployment's own (ADR-20261104120000). A stage's
// events stay in its stage and production's in production. The guest may
// name its environment, which must then be its own; it cannot publish into
// another environment.
func guestEventEnvironment(ctx context.Context, store any, accountID, appID, deploymentID string, body []byte) (string, error) {
	var named struct {
		EnvironmentID string `json:"environmentid"`
	}
	if err := json.Unmarshal(body, &named); err != nil {
		return "", fmt.Errorf("decode in-guest event publish: %w", err)
	}
	requested := strings.TrimSpace(named.EnvironmentID)
	environment := ""
	if deploymentID != "" {
		resolver, ok := store.(state.EventEnvironmentStore)
		if !ok {
			return "", errors.New("event environment resolution is not configured")
		}
		var err error
		if environment, err = resolver.DeploymentEventEnvironment(ctx, accountID, appID, deploymentID); err != nil {
			return "", fmt.Errorf("resolve in-guest event environment: %w", err)
		}
	}
	if requested != "" && !strings.EqualFold(requested, environment) {
		if resolver, ok := store.(state.EventEnvironmentStore); ok && environment == "" {
			// Naming production by its environment ID is still production.
			if resolved, err := resolver.AccountEventEnvironment(ctx, accountID, requested); err == nil && resolved == "" {
				return "", nil
			}
		}
		return "", fmt.Errorf("validate in-guest event publish: %w: environmentid must be the publishing deployment's environment", events.ErrPublishInvalid)
	}
	return environment, nil
}
