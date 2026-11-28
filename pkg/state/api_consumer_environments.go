package state

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
)

// API consumers and their keys belong to one environment of the app's
// project; production is the empty environment (ADR-20261104120000
// amendment 2026-11-22). The app-wide methods (ListAPIConsumersForApp,
// ListConsumerKeysForApp, ConsumerKeyByAppAndPrefix) stay production's, so
// consumer plans, signups, statements and billing never see a stage's.

// APIConsumerEnvironmentStore reads and writes a stage's consumers. The
// environment is a slug of the app's project; "" and "production" name
// production. A missing environment is ErrNotFound.
type APIConsumerEnvironmentStore interface {
	CreateAPIConsumerInEnvironment(ctx context.Context, accountID, appID, environment, externalRef, name string) (APIConsumer, error)
	ListAPIConsumersInEnvironment(ctx context.Context, accountID, appID, environment string) ([]APIConsumer, error)
	// ListConsumerKeysForConsumer lists one consumer's keys, whichever
	// environment it belongs to.
	ListConsumerKeysForConsumer(ctx context.Context, accountID, consumerID string) ([]ConsumerKey, error)
	// ConsumerKeyForEnvironment is the gateway's lookup for a request to an
	// environment's host: a stage that holds consumers of its own answers
	// with its keys only; one with none, and production, answer with
	// production's.
	ConsumerKeyForEnvironment(ctx context.Context, accountID, appID, environmentID, prefix string) (ConsumerKey, error)
}

// ErrAPIConsumerStageTrial refuses a trial for a stage's consumer: a stage's
// consumers are not billed, so a trial means nothing there. Plan assignments
// follow their consumer.
var ErrAPIConsumerStageTrial = fmt.Errorf("%w: trials apply to production consumers; a stage's consumers are not billed", ErrConflict)

// productionConsumerEnvironment reports whether a slug names production.
func productionConsumerEnvironment(environment string) bool {
	return environment == "" || IsProductionScope(environment)
}

// consumerEnvironmentIDLocked resolves an app's project environment slug.
func (m *MemStore) consumerEnvironmentIDLocked(accountID, appID, environment string) (string, error) {
	if productionConsumerEnvironment(environment) {
		return "", nil
	}
	app, ok := m.apps[appID]
	if !ok || app.AccountID != accountID || app.ProjectID == "" {
		return "", ErrNotFound
	}
	env, err := m.projectEnvironmentBySlugLocked(app.ProjectID, environment)
	if err != nil {
		return "", err
	}
	return env.ID, nil
}

func (m *MemStore) CreateAPIConsumerInEnvironment(ctx context.Context, accountID, appID, environment, externalRef, name string) (APIConsumer, error) {
	if productionConsumerEnvironment(environment) {
		return m.CreateAPIConsumer(ctx, accountID, appID, externalRef, name)
	}
	if err := validateAPIConsumerInput("CreateAPIConsumerInEnvironment", accountID, appID, externalRef, name); err != nil {
		return APIConsumer{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	environmentID, err := m.consumerEnvironmentIDLocked(accountID, appID, environment)
	if err != nil {
		return APIConsumer{}, err
	}
	// External references are unique per app and environment
	// (ADR-20261104120000).
	for _, c := range m.apiConsumers {
		if c.AppID == appID && c.EnvironmentID == environmentID && c.ExternalRef == externalRef {
			return APIConsumer{}, ErrConflict
		}
	}
	now := time.Now().UTC()
	c := APIConsumer{ID: uuid.NewString(), AccountID: accountID, AppID: appID, ExternalRef: externalRef, Name: name,
		Status: APIConsumerStatusActive, CreatedAt: now, UpdatedAt: now, EnvironmentID: environmentID}
	m.apiConsumers[c.ID] = c
	return c, nil
}

func (m *MemStore) ListAPIConsumersInEnvironment(ctx context.Context, accountID, appID, environment string) ([]APIConsumer, error) {
	if productionConsumerEnvironment(environment) {
		return m.ListAPIConsumersForApp(ctx, accountID, appID)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	environmentID, err := m.consumerEnvironmentIDLocked(accountID, appID, environment)
	if err != nil {
		return nil, err
	}
	var out []APIConsumer
	for _, c := range m.apiConsumers {
		if c.AccountID == accountID && c.AppID == appID && c.EnvironmentID == environmentID {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

func (m *MemStore) ListConsumerKeysForConsumer(_ context.Context, accountID, consumerID string) ([]ConsumerKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.apiConsumers[consumerID]
	if !ok || c.AccountID != accountID {
		return nil, ErrNotFound
	}
	var out []ConsumerKey
	for _, k := range m.consumerKeys {
		if k.AccountID == accountID && k.ConsumerID == consumerID {
			out = append(out, k)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

// stageConsumerLocked reports whether a consumer belongs to a stage.
func (m *MemStore) stageConsumerLocked(consumerID string) bool {
	c, ok := m.apiConsumers[consumerID]
	return ok && c.EnvironmentID != ""
}

func (m *MemStore) ConsumerKeyForEnvironment(_ context.Context, accountID, appID, environmentID, prefix string) (ConsumerKey, error) {
	if accountID == "" || appID == "" || prefix == "" {
		return ConsumerKey{}, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	stage := m.stageConsumerEnvironmentLocked(accountID, appID, environmentID)
	for _, k := range m.consumerKeys {
		if k.AccountID == accountID && k.AppID == appID && k.Prefix == prefix && k.EnvironmentID == stage {
			if tenantID := m.platformTenantByConsumer[k.ConsumerID]; tenantID != "" {
				if tenant, ok := m.platformTenants[tenantID]; ok && tenant.Status == PlatformTenantSuspended {
					return ConsumerKey{}, ErrNotFound
				}
			}
			return k, nil
		}
	}
	return ConsumerKey{}, ErrNotFound
}

// stageConsumerEnvironmentLocked is the environment whose keys answer a
// request to environmentID's host: the stage when it holds active consumers
// of its own, otherwise "" (production).
func (m *MemStore) stageConsumerEnvironmentLocked(accountID, appID, environmentID string) string {
	if environmentID == "" {
		return ""
	}
	env, ok := m.projectEnvironments[environmentID]
	app, found := m.apps[appID]
	if !ok || !found || IsProductionScope(env.Slug) || env.ProjectID != app.ProjectID || env.AccountID != accountID || app.AccountID != accountID {
		return ""
	}
	for _, c := range m.apiConsumers {
		if c.AppID == appID && c.EnvironmentID == environmentID && c.Active() {
			return environmentID
		}
	}
	return ""
}
