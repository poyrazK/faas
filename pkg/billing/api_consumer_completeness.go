package billing

import (
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// Completeness statuses (ADR-938).
const (
	// CompletenessVerified: telemetry independently confirms every billed
	// successful request in the checked hours, and saw none the ledger lacks.
	CompletenessVerified = "verified"
	// CompletenessPartial: no gaps found, but telemetry covers only part of
	// the billed requests (it is sampled under pressure, can be switched off,
	// and is retained for 14 days).
	CompletenessPartial = "partial"
	// CompletenessGapsDetected: telemetry saw successful requests the
	// billing ledger never received, so the ledger undercounts.
	CompletenessGapsDetected = "gaps_detected"
	// CompletenessUnverifiable: billed requests exist but telemetry holds no
	// evidence for them.
	CompletenessUnverifiable = "unverifiable"
)

// APIConsumerUsageCompleteness compares a consumer's billing ledger with
// request telemetry, hour by hour, over successful (status < 400) requests.
// Requests Gregale deliberately leaves unbilled (admission denials,
// platform failures) are errors, so both sides exclude them alike.
// Telemetry is lossy, so it can only prove a lower bound of missing usage:
// an hour where it saw more successful requests than the ledger billed.
type APIConsumerUsageCompleteness struct {
	Status                string
	CheckedFrom           time.Time
	CheckedUntil          time.Time
	LedgerRequests        int64
	TelemetryRequests     int64
	ConfirmedRequests     int64
	MissingRequests       int64
	HoursChecked          int
	HoursWithoutTelemetry int
}

// CheckAPIConsumerUsageCompleteness evaluates [from, until), which callers
// align to whole UTC hours that telemetry has had time to settle.
func CheckAPIConsumerUsageCompleteness(usage []state.APIConsumerUsageBucket, telemetry []state.APIConsumerTelemetryHour, from, until time.Time) APIConsumerUsageCompleteness {
	result := APIConsumerUsageCompleteness{CheckedFrom: from, CheckedUntil: until}
	ledger := map[int64]int64{}
	for _, bucket := range usage {
		minute := bucket.WindowStart.UTC()
		if minute.Before(from) || !minute.Before(until) {
			continue
		}
		ledger[minute.Truncate(time.Hour).Unix()] += bucket.RequestCount - bucket.ErrorCount
	}
	observed := map[int64]int64{}
	for _, hour := range telemetry {
		at := hour.Hour.UTC().Truncate(time.Hour)
		if at.Before(from) || !at.Before(until) {
			continue
		}
		observed[at.Unix()] += hour.SuccessfulRequests
	}
	hours := map[int64]bool{}
	for h := range ledger {
		hours[h] = true
	}
	for h := range observed {
		hours[h] = true
	}
	for h := range hours {
		billed, seen := ledger[h], observed[h]
		result.HoursChecked++
		result.LedgerRequests += billed
		result.TelemetryRequests += seen
		result.ConfirmedRequests += min(billed, seen)
		result.MissingRequests += max(0, seen-billed)
		if billed > 0 && seen == 0 {
			result.HoursWithoutTelemetry++
		}
	}
	switch {
	case result.MissingRequests > 0:
		result.Status = CompletenessGapsDetected
	case result.LedgerRequests > 0 && result.TelemetryRequests == 0:
		result.Status = CompletenessUnverifiable
	case result.ConfirmedRequests < result.LedgerRequests:
		result.Status = CompletenessPartial
	default:
		result.Status = CompletenessVerified
	}
	return result
}
