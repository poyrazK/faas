package api

import (
	"fmt"
	"regexp"
	"time"
)

var appEventAcceptanceTimestamp = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]{1,9})?(?:Z|[+-](?:[01][0-9]|2[0-3]):[0-5][0-9])$`)

// AppEventAcceptanceGuard pins the saved receipt's precise acceptance instant.
type AppEventAcceptanceGuard struct {
	ExpectedAcceptedAt *time.Time `json:"expected_accepted_at,omitempty"`
}

func (g AppEventAcceptanceGuard) Validate() error {
	if g.ExpectedAcceptedAt != nil && (g.ExpectedAcceptedAt.IsZero() || g.ExpectedAcceptedAt.UTC().Year() < 1 || g.ExpectedAcceptedAt.UTC().Year() > 9999) {
		return fmt.Errorf("expected_accepted_at must be a nonzero RFC3339 timestamp")
	}
	return nil
}
func ParseAppEventAcceptanceGuard(raw string) (AppEventAcceptanceGuard, error) {
	var guard AppEventAcceptanceGuard
	if raw == "" {
		return guard, nil
	}
	if !appEventAcceptanceTimestamp.MatchString(raw) {
		return guard, fmt.Errorf("expected_accepted_at must be RFC3339 with at most nanosecond precision")
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return guard, fmt.Errorf("expected_accepted_at must be RFC3339 with its original precision")
	}
	parsed = parsed.UTC()
	guard.ExpectedAcceptedAt = &parsed
	return guard, guard.Validate()
}
func (g AppEventAcceptanceGuard) Compare(acceptedAt *time.Time) string {
	if g.ExpectedAcceptedAt == nil {
		return ""
	}
	if acceptedAt == nil {
		return "unavailable"
	}
	if g.ExpectedAcceptedAt.Equal(*acceptedAt) {
		return "same_acceptance"
	}
	return "replacement_acceptance"
}
