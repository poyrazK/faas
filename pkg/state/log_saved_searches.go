package state

import (
	"context"
	"fmt"
	"regexp"
	"time"
)

// LogSavedSearch is one app's named ledger filter (ADR-20261018083536 slice
// D). Query is a canonical CanonicalLogQuery string: filters only, no window,
// so the search, the dashboard and an alert each bring their own.
type LogSavedSearch struct {
	ID        string
	AccountID string
	AppID     string
	Name      string
	Query     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// LogSavedSearchStore keeps an app's saved searches. apid is the only writer.
type LogSavedSearchStore interface {
	// PutLogSavedSearch creates the search or replaces the query of the
	// app's search with the same name. Creating one past limit returns a
	// *LogSavedSearchQuotaError; replacing never counts.
	PutLogSavedSearch(ctx context.Context, search LogSavedSearch, limit int) (LogSavedSearch, error)
	// ListLogSavedSearches returns the app's searches by name.
	ListLogSavedSearches(ctx context.Context, accountID, appID string) ([]LogSavedSearch, error)
	// LogSavedSearchByName returns ErrNotFound when the app has no such search.
	LogSavedSearchByName(ctx context.Context, accountID, appID, name string) (LogSavedSearch, error)
	// DeleteLogSavedSearch returns ErrNotFound when the app has no such search.
	DeleteLogSavedSearch(ctx context.Context, accountID, appID, name string) error
}

// LogSavedSearchQuotaError reports the per-app cap on saved searches.
type LogSavedSearchQuotaError struct {
	Limit    int
	Observed int
}

func (e *LogSavedSearchQuotaError) Error() string {
	return fmt.Sprintf("state: app has %d of %d saved log searches", e.Observed, e.Limit)
}

// logSavedSearchName is log_saved_searches_name_shape.
var logSavedSearchName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// ValidLogSavedSearchName reports whether name fits the stored name shape.
func ValidLogSavedSearchName(name string) bool { return logSavedSearchName.MatchString(name) }

const logSavedSearchQueryMaxBytes = 2048

func validateLogSavedSearch(search LogSavedSearch) error {
	if search.AccountID == "" || search.AppID == "" {
		return fmt.Errorf("state: saved log search requires an account and an app")
	}
	if !ValidLogSavedSearchName(search.Name) {
		return fmt.Errorf("state: saved log search name %q must match %s", search.Name, logSavedSearchName)
	}
	if len(search.Query) > logSavedSearchQueryMaxBytes {
		return fmt.Errorf("state: saved log search query exceeds %d bytes", logSavedSearchQueryMaxBytes)
	}
	return nil
}
