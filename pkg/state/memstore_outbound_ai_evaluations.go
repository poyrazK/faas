package state

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ OutboundAIEvaluationStore = (*MemStore)(nil)

// CreateOutboundAIEvaluation mirrors the PgStore checks (ADR-1431). Nothing
// runs memory evaluations; tests seed results with SeedOutboundAIEvaluation.
func (m *MemStore) CreateOutboundAIEvaluation(_ context.Context, evaluation OutboundAIEvaluation) (OutboundAIEvaluation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	route, ok := m.outboundAIRoutes[evaluation.RouteID]
	if !ok || route.AccountID != evaluation.AccountID {
		return OutboundAIEvaluation{}, ErrNotFound
	}
	if evaluation.Position < 2 || evaluation.Position > len(route.Targets) {
		return OutboundAIEvaluation{}, ErrInvalidArgument
	}
	judge, ok := m.outboundIntegrationOffers[evaluation.JudgeIntegrationID]
	if !ok || judge.AccountID != evaluation.AccountID || !judge.Enabled || judge.AIProvider == "" || judge.OwnerKind != "customer" {
		return OutboundAIEvaluation{}, ErrInvalidArgument
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	count := 0
	for _, existing := range m.outboundAIEvaluations {
		if existing.RouteID == evaluation.RouteID && !existing.CreatedAt.Before(today) {
			count++
		}
	}
	if count >= api.OutboundAIEvaluationsPerDay {
		return OutboundAIEvaluation{}, ErrOutboundAIEvaluationLimit
	}
	if evaluation.ID == "" {
		evaluation.ID = uuid.NewString()
	}
	evaluation.State, evaluation.CreatedAt, evaluation.Items = api.OutboundAIEvaluationPending, time.Now().UTC(), nil
	evaluation.Canary = copyOutboundAIEvaluationCanary(evaluation.Canary)
	if m.outboundAIEvaluations == nil {
		m.outboundAIEvaluations = make(map[string]OutboundAIEvaluation)
	}
	m.outboundAIEvaluations[evaluation.ID] = evaluation
	return copyOutboundAIEvaluation(evaluation), nil
}

func (m *MemStore) ListOutboundAIEvaluations(_ context.Context, accountID, routeID string) ([]OutboundAIEvaluation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]OutboundAIEvaluation, 0)
	for _, evaluation := range m.outboundAIEvaluations {
		if evaluation.AccountID == accountID && evaluation.RouteID == routeID {
			evaluation = copyOutboundAIEvaluation(evaluation)
			evaluation.Items = nil
			out = append(out, evaluation)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if len(out) > outboundAIEvaluationsListMax {
		out = out[:outboundAIEvaluationsListMax]
	}
	return out, nil
}

func (m *MemStore) GetOutboundAIEvaluation(_ context.Context, accountID, evaluationID string) (OutboundAIEvaluation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	evaluation, ok := m.outboundAIEvaluations[evaluationID]
	if !ok || evaluation.AccountID != accountID {
		return OutboundAIEvaluation{}, ErrNotFound
	}
	return copyOutboundAIEvaluation(evaluation), nil
}

// SeedOutboundAIEvaluation replaces a stored evaluation, as outboundd's
// worker would finish it.
func (m *MemStore) SeedOutboundAIEvaluation(evaluation OutboundAIEvaluation) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.outboundAIEvaluations == nil {
		m.outboundAIEvaluations = make(map[string]OutboundAIEvaluation)
	}
	m.outboundAIEvaluations[evaluation.ID] = copyOutboundAIEvaluation(evaluation)
}

func copyOutboundAIEvaluation(in OutboundAIEvaluation) OutboundAIEvaluation {
	out := in
	out.Canary = copyOutboundAIEvaluationCanary(in.Canary)
	out.Items = append([]OutboundAIEvaluationItem(nil), in.Items...)
	return out
}

func copyOutboundAIEvaluationCanary(in *api.PutOutboundAIRouteCanaryRequest) *api.PutOutboundAIRouteCanaryRequest {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}
