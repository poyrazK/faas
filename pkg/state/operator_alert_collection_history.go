package state

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type OperatorAlertCollectionOutcome struct {
	AttemptedAt time.Time
	Outcome     string
	Reason      string
}

type OperatorAlertCollectionHistoryFilter struct {
	Since, Now     time.Time
	IncludeDetails bool
}

type OperatorAlertCollectionHistorySnapshot struct {
	Collection OperatorAlertCollection
	Outcomes   []OperatorAlertCollectionOutcome
	Failures   []api.ObsAlertCollectionFailure
}

type OperatorAlertCollectionHistoryReader interface {
	OperatorAlertCollectionHistory(context.Context, OperatorAlertCollectionHistoryFilter) (OperatorAlertCollectionHistorySnapshot, error)
}

type OperatorAlertCollectionHistoryLimitError struct{ Limit int }

func (e *OperatorAlertCollectionHistoryLimitError) Error() string {
	return fmt.Sprintf("collection outcomes exceed %d; narrow since", e.Limit)
}

func normalizeCollectionHistoryFilter(f OperatorAlertCollectionHistoryFilter) (OperatorAlertCollectionHistoryFilter, error) {
	f.Now, f.Since = f.Now.UTC().Truncate(time.Microsecond), f.Since.UTC().Truncate(time.Microsecond)
	if f.Now.IsZero() || f.Since.IsZero() || !f.Since.Before(f.Now) || f.Since.Before(f.Now.Add(-api.ObsAlertCollectionHistoryRetention)) {
		return f, ErrInvalidArgument
	}
	return f, nil
}

func collectionOutcomeReadSince(since, now time.Time) time.Time {
	from := since.Add(-api.ObsAlertCollectionOutcomeMaxAge)
	if cutoff := now.Add(-api.ObsAlertCollectionHistoryRetention); from.Before(cutoff) {
		from = cutoff
	}
	return from
}

func collectionFailureReason(reasons []string) (string, error) {
	if len(reasons) == 0 {
		return "collection_failed", nil
	}
	if len(reasons) == 1 {
		switch reasons[0] {
		case "collection_failed", "source_read_failed", "snapshot_rejected", "timeout", "persistence_failed":
			return reasons[0], nil
		}
	}
	return "", ErrInvalidArgument
}
