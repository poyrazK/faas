package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type APISandboxScenarioStore interface {
	GetAPISandboxScenario(ctx context.Context, deploymentID string) (api.APISandboxScenario, error)
	PutAPISandboxScenario(ctx context.Context, deploymentID string, input api.APISandboxScenarioInput) (api.APISandboxScenario, error)
	PutAPISandboxScenarioFromRequest(ctx context.Context, deploymentID string, input api.APISandboxScenarioInput, origin APISandboxScenarioOrigin) (api.APISandboxScenario, error)
}

type APISandboxScenarioOrigin struct {
	RequestID        string
	ExpectedRevision string
}

type savedAPISandboxScenario struct {
	revision        string
	definition      []byte
	sourceRequestID string
}

func (saved savedAPISandboxScenario) decode() (api.APISandboxScenario, error) {
	scenario := api.APISandboxScenario{Revision: saved.revision, SourceRequestID: saved.sourceRequestID}
	if err := json.Unmarshal(saved.definition, &scenario.APISandboxScenarioInput); err != nil {
		return api.APISandboxScenario{}, fmt.Errorf("state: decode saved sandbox scenario: %w", err)
	}
	return scenario, nil
}

func (s *PgStore) GetAPISandboxScenario(ctx context.Context, deploymentID string) (api.APISandboxScenario, error) {
	row, err := sqlc.New().GetAPISandboxScenario(ctx, s.pool, mustPgUUID(deploymentID))
	if err != nil {
		return api.APISandboxScenario{}, mapErr(err)
	}
	return savedAPISandboxScenario{pgUUIDString(row.Revision), row.Definition, pgUUIDString(row.SourceRequestID)}.decode()
}

func (s *PgStore) PutAPISandboxScenario(ctx context.Context, deploymentID string, input api.APISandboxScenarioInput) (api.APISandboxScenario, error) {
	return s.PutAPISandboxScenarioFromRequest(ctx, deploymentID, input, APISandboxScenarioOrigin{})
}

func (s *PgStore) PutAPISandboxScenarioFromRequest(ctx context.Context, deploymentID string, input api.APISandboxScenarioInput, origin APISandboxScenarioOrigin) (api.APISandboxScenario, error) {
	definition, err := json.Marshal(input)
	if err != nil {
		return api.APISandboxScenario{}, fmt.Errorf("state: encode sandbox scenario: %w", err)
	}
	var requestID, expectedRevision pgtype.UUID
	if origin.RequestID != "" {
		requestID = mustPgUUID(origin.RequestID)
	}
	if origin.ExpectedRevision != "" {
		expectedRevision = mustPgUUID(origin.ExpectedRevision)
	}
	row, err := sqlc.New().PutAPISandboxScenario(ctx, s.pool, sqlc.PutAPISandboxScenarioParams{
		DeploymentID: mustPgUUID(deploymentID), Revision: mustPgUUID(uuid.NewString()), Definition: definition,
		SourceRequestID: requestID, ExpectedRevision: expectedRevision,
	})
	if err != nil {
		err = mapErr(err)
		if errors.Is(err, ErrNotFound) && origin.RequestID != "" {
			return api.APISandboxScenario{}, ErrConflict
		}
		return api.APISandboxScenario{}, err
	}
	return savedAPISandboxScenario{pgUUIDString(row.Revision), row.Definition, pgUUIDString(row.SourceRequestID)}.decode()
}

func (m *MemStore) GetAPISandboxScenario(_ context.Context, deploymentID string) (api.APISandboxScenario, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	scenario, ok := m.apiSandboxScenarios[deploymentID]
	if !ok {
		return api.APISandboxScenario{}, ErrNotFound
	}
	return scenario.decode()
}

func (m *MemStore) PutAPISandboxScenario(ctx context.Context, deploymentID string, input api.APISandboxScenarioInput) (api.APISandboxScenario, error) {
	return m.PutAPISandboxScenarioFromRequest(ctx, deploymentID, input, APISandboxScenarioOrigin{})
}

func (m *MemStore) PutAPISandboxScenarioFromRequest(ctx context.Context, deploymentID string, input api.APISandboxScenarioInput, origin APISandboxScenarioOrigin) (api.APISandboxScenario, error) {
	definition, err := json.Marshal(input)
	if err != nil {
		return api.APISandboxScenario{}, fmt.Errorf("state: encode sandbox scenario: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return api.APISandboxScenario{}, err
	}
	if _, ok := m.deployments[deploymentID]; !ok {
		return api.APISandboxScenario{}, ErrNotFound
	}
	saved := m.apiSandboxScenarios[deploymentID]
	requestID := saved.sourceRequestID
	if origin.RequestID != "" {
		if saved.revision != origin.ExpectedRevision {
			return api.APISandboxScenario{}, ErrConflict
		}
		requestID = origin.RequestID
	}
	if !bytes.Equal(saved.definition, definition) || requestID != saved.sourceRequestID {
		if m.apiSandboxScenarios == nil {
			m.apiSandboxScenarios = make(map[string]savedAPISandboxScenario)
		}
		saved = savedAPISandboxScenario{uuid.NewString(), definition, requestID}
		m.apiSandboxScenarios[deploymentID] = saved
	}
	return saved.decode()
}

func cloneAPISandboxScenario(scenario *api.APISandboxScenario) *api.APISandboxScenario {
	if scenario == nil {
		return nil
	}
	copy := *scenario
	copy.SourceRequestID = ""
	copy.Seed = slices.Clone(scenario.Seed)
	if scenario.StripeWebhook != nil {
		webhook := *scenario.StripeWebhook
		webhook.DataObject = slices.Clone(webhook.DataObject)
		copy.StripeWebhook = &webhook
	}
	copy.SeededIDs = maps.Clone(scenario.SeededIDs)
	if scenario.OutboundWebhook != nil {
		webhook := *scenario.OutboundWebhook
		webhook.Assertions = slices.Clone(webhook.Assertions)
		for i := range webhook.Assertions {
			webhook.Assertions[i].Equals = slices.Clone(webhook.Assertions[i].Equals)
		}
		copy.OutboundWebhook = &webhook
	}
	copy.Requests = slices.Clone(scenario.Requests)
	for i := range copy.Requests {
		copy.Requests[i].Body = slices.Clone(copy.Requests[i].Body)
		copy.Requests[i].Assertions = slices.Clone(copy.Requests[i].Assertions)
		for j := range copy.Requests[i].Assertions {
			copy.Requests[i].Assertions[j].Equals = slices.Clone(copy.Requests[i].Assertions[j].Equals)
		}
	}
	return &copy
}
