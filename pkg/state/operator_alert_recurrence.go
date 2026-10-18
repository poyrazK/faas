package state

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type OperatorAlertRecurrenceFilter struct {
	Since, Now        time.Time
	CollectionEnabled bool
}

type OperatorAlertRecurrenceEpisode struct {
	ID, RuleKey, SeriesID, Name, Severity, Component, Status string
	AccountID, AppID, NodeID                                 string
	StartedAt                                                time.Time
	ResolvedAt                                               *time.Time
}

type OperatorAlertRecurrenceSnapshot struct {
	CollectionOutcomes []OperatorAlertCollectionOutcome
	Collection         OperatorAlertCollection
	Retention          OperatorAlertRetention
	Episodes           []OperatorAlertRecurrenceEpisode
}

type OperatorAlertRecurrenceReader interface {
	OperatorAlertRecurrence(context.Context, OperatorAlertRecurrenceFilter) (OperatorAlertRecurrenceSnapshot, error)
}

type OperatorAlertRecurrenceLimitError struct{ Limit int }

func (e *OperatorAlertRecurrenceLimitError) Error() string {
	return fmt.Sprintf("alert recurrence episodes exceed %d; narrow since", e.Limit)
}

func normalizeAlertRecurrenceFilter(f OperatorAlertRecurrenceFilter) (OperatorAlertRecurrenceFilter, error) {
	f.Now = f.Now.UTC().Truncate(time.Microsecond)
	f.Since = f.Since.UTC().Truncate(time.Microsecond)
	if f.Now.IsZero() || f.Since.IsZero() || !f.Since.Before(f.Now) || f.Since.Before(f.Now.Add(-api.ObsAlertRecurrenceWindowMax)) {
		return f, ErrInvalidArgument
	}
	return f, nil
}
