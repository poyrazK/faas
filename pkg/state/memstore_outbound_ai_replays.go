package state

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ OutboundAIReplayStore = (*MemStore)(nil)

// CreateOutboundAIReplay mirrors the PgStore checks (ADR-997). Nothing runs
// memory replays; tests seed results with SeedOutboundAIReplayResults.
func (m *MemStore) CreateOutboundAIReplay(_ context.Context, replay OutboundAIReplay) (OutboundAIReplay, error) {
	if len(replay.Targets) == 0 || len(replay.Targets) > api.OutboundAIReplayTargetsMax {
		return OutboundAIReplay{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	source, ok := m.outboundIntegrationOffers[replay.IntegrationID]
	if !ok || source.AccountID != replay.AccountID || source.OwnerKind != "customer" || source.AIProvider == "" {
		return OutboundAIReplay{}, ErrNotFound
	}
	var entry OutboundAIRequestLogEntry
	for _, logged := range m.outboundAIRequestLog[replay.IntegrationID] {
		if logged.ID == replay.LogID {
			entry = logged
		}
	}
	if entry.ID == "" {
		return OutboundAIReplay{}, ErrNotFound
	}
	content, ok := m.outboundAIRequestContent[replay.IntegrationID+"/"+replay.LogID]
	if !ok || content.RequestTruncated || content.RequestPath == "" {
		return OutboundAIReplay{}, ErrOutboundAIReplayUnavailable
	}
	for _, target := range replay.Targets {
		offer, ok := m.outboundIntegrationOffers[target.IntegrationID]
		if !ok || offer.AccountID != replay.AccountID || offer.OwnerKind != "customer" || offer.AIProvider == "" ||
			(offer.AIProvider != source.AIProvider && target.Model == "") {
			return OutboundAIReplay{}, ErrInvalidArgument
		}
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	count := 0
	for _, existing := range m.outboundAIReplays {
		if existing.IntegrationID == replay.IntegrationID && !existing.CreatedAt.Before(today) {
			count++
		}
	}
	if count >= api.OutboundAIReplaysPerDay {
		return OutboundAIReplay{}, ErrOutboundAIReplayLimit
	}
	replay.ID, replay.State, replay.CreatedAt, replay.Source = uuid.NewString(), api.OutboundAIReplayPending, time.Now().UTC(), entry
	replay.Targets = append([]OutboundAIReplayTarget(nil), replay.Targets...)
	if m.outboundAIReplays == nil {
		m.outboundAIReplays = make(map[string]OutboundAIReplay)
	}
	m.outboundAIReplays[replay.ID] = replay
	return replay, nil
}

// GetOutboundAIReplay reads one of the account's replays.
func (m *MemStore) GetOutboundAIReplay(_ context.Context, accountID, replayID string) (OutboundAIReplay, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	replay, ok := m.outboundAIReplays[replayID]
	if !ok || replay.AccountID != accountID {
		return OutboundAIReplay{}, ErrNotFound
	}
	replay.Results = append([]OutboundAIReplayResult{}, replay.Results...)
	return replay, nil
}

// SeedOutboundAIReplayResults stands in for outboundd finishing a replay.
func (m *MemStore) SeedOutboundAIReplayResults(replayID, sourceOutputText string, results ...OutboundAIReplayResult) {
	m.mu.Lock()
	defer m.mu.Unlock()
	replay, ok := m.outboundAIReplays[replayID]
	if !ok {
		return
	}
	finished := time.Now().UTC()
	replay.State, replay.SourceOutputText, replay.FinishedAt = api.OutboundAIReplayDone, sourceOutputText, &finished
	replay.Results = append(replay.Results, results...)
	m.outboundAIReplays[replayID] = replay
}
