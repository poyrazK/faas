package state

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	filtersqlc "github.com/onebox-faas/faas/pkg/state/realtimebulk/sqlc"
)

var ErrRealtimeConsumerGroupFilterScanLimited = errors.New("state: realtime consumer group filter scan budget reached")

type RealtimeConsumerGroupFilter struct {
	EventTypes []string          `json:"event_types"`
	Metadata   map[string]string `json:"metadata"`
}

func normalizeRealtimeGroupFilter(input *RealtimeConsumerGroupFilter) (RealtimeConsumerGroupFilter, error) {
	result := RealtimeConsumerGroupFilter{EventTypes: []string{}, Metadata: map[string]string{}}
	if input == nil {
		return result, nil
	}
	if len(input.EventTypes) > api.RealtimeConsumerGroupFilterEventTypesMax || api.ValidateRealtimeMetadata(input.Metadata) != nil {
		return result, ErrRealtimeConsumerGroupInvalid
	}
	seen := map[string]bool{}
	for _, event := range input.EventTypes {
		if !validRealtimeWorkerName(event, api.RealtimePublisherEventTypeMaxBytes) || seen[event] {
			return result, ErrRealtimeConsumerGroupInvalid
		}
		seen[event] = true
		result.EventTypes = append(result.EventTypes, event)
	}
	for key, value := range input.Metadata {
		result.Metadata[key] = value
	}
	data, err := json.Marshal(result)
	if err != nil || len(data) > api.RealtimeConsumerGroupFilterMaxBytes {
		return result, ErrRealtimeConsumerGroupInvalid
	}
	return result, nil
}
func realtimeGroupFilterMatches(filter RealtimeConsumerGroupFilter, metadata map[string]string) bool {
	if len(filter.EventTypes) > 0 {
		matched := false
		for _, event := range filter.EventTypes {
			if actual, exists := metadata["event_type"]; exists && actual == event {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	for key, expected := range filter.Metadata {
		actual, exists := metadata[key]
		if !exists || actual != expected {
			return false
		}
	}
	return true
}
func skipRealtimeGroupFilteredMessage(ctx context.Context, tx pgx.Tx, id string, sequence int64) error {
	return filtersqlc.New().SkipFilteredMessage(ctx, tx, filtersqlc.SkipFilteredMessageParams{GroupID: id, Sequence: sequence})
}
func configureRealtimeGroupFilter(ctx context.Context, tx pgx.Tx, id string, filter RealtimeConsumerGroupFilter) error {
	data, err := json.Marshal(filter)
	if err != nil {
		return err
	}
	return filtersqlc.New().SetInitialMessageFilter(ctx, tx, filtersqlc.SetInitialMessageFilterParams{GroupID: id, MessageFilter: data})
}
