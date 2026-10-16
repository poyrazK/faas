package state

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type OperatorAlertResponseTimesFilter struct {
	Since, Now        time.Time
	CollectionEnabled bool
}
type OperatorAlertResponseTimesEpisode struct {
	ID, Severity, Component, Status string
	StartedAt                       time.Time
	ResolvedAt                      *time.Time
	Revision                        int64
	ReceiptCoverage                 string
	MissingRevisions                int64
	FirstAcknowledgedAt             *time.Time
	AckClockInvalid                 bool
}
type OperatorAlertResponseTimesSnapshot struct {
	CollectionOutcomes []OperatorAlertCollectionOutcome
	Collection         OperatorAlertCollection
	Retention          OperatorAlertRetention
	Episodes           []OperatorAlertResponseTimesEpisode
}
type OperatorAlertResponseTimesReader interface {
	OperatorAlertResponseTimes(context.Context, OperatorAlertResponseTimesFilter) (OperatorAlertResponseTimesSnapshot, error)
}

// Bounds reject a complete read rather than silently sampling its cohort.
type OperatorAlertResponseTimesLimitError struct {
	Resource string
	Limit    int
}

func (e *OperatorAlertResponseTimesLimitError) Error() string {
	return fmt.Sprintf("alert response timing %s exceed %d; narrow since", e.Resource, e.Limit)
}
func normalizeAlertResponseTimesFilter(f OperatorAlertResponseTimesFilter) (OperatorAlertResponseTimesFilter, error) {
	f.Now = f.Now.UTC().Truncate(time.Microsecond)
	f.Since = f.Since.UTC().Truncate(time.Microsecond)
	if f.Now.IsZero() || f.Since.IsZero() || !f.Since.Before(f.Now) || f.Since.Before(f.Now.Add(-api.ObsAlertResponseTimesWindowMax)) {
		return f, ErrInvalidArgument
	}
	return f, nil
}

type alertTimingReceipt struct {
	EpisodeID, ID string
	At            time.Time
	Payload       []byte
}

// Closed payloads are decoded before any result leaves the store. Coverage is
// based on the saved revision; clocks never determine which action was first.
func applyAlertTimingReceipts(episodes []OperatorAlertResponseTimesEpisode, receipts []alertTimingReceipt, now time.Time) error {
	byEpisode := map[string][]alertTimingReceipt{}
	for _, r := range receipts {
		byEpisode[r.EpisodeID] = append(byEpisode[r.EpisodeID], r)
	}
	for i := range episodes {
		e := &episodes[i]
		if e.Revision < 0 || e.Revision > api.ObsAlertTriageRevisionMax {
			return errAlertTriageReceipt
		}
		revisions := map[int64]bool{}
		entries := []api.ObsAlertTriageHistoryEntry{}
		invalid := false
		for _, r := range byEpisode[e.ID] {
			var header struct {
				Triage struct {
					Revision json.RawMessage `json:"revision"`
				} `json:"triage"`
			}
			if err := json.Unmarshal(r.Payload, &header); err != nil {
				invalid = true
				continue
			}
			revision, err := strconv.ParseInt(string(header.Triage.Revision), 10, 64)
			if err != nil || revision < 1 || revision > e.Revision || strconv.FormatInt(revision, 10) != string(header.Triage.Revision) {
				invalid = true
				continue
			}
			if revisions[revision] {
				invalid = true
			}
			revisions[revision] = true
			entry, err := decodeAlertTriageReceipt(r.ID, e.ID, r.At, r.Payload)
			if err != nil {
				invalid = true
				continue
			}
			entries = append(entries, entry)
		}
		e.MissingRevisions = e.Revision - int64(len(revisions))
		e.ReceiptCoverage = "complete"
		if invalid {
			e.ReceiptCoverage = "invalid"
			continue
		}
		if e.MissingRevisions > 0 {
			e.ReceiptCoverage = "gaps"
			continue
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Revision < entries[j].Revision })
		var previous time.Time
		for _, entry := range entries {
			if entry.At.Before(e.StartedAt) || entry.At.After(now) || !previous.IsZero() && entry.At.Before(previous) {
				e.AckClockInvalid = true
			}
			previous = entry.At
			if e.FirstAcknowledgedAt == nil && entry.Action == "acknowledge" {
				at := entry.At
				e.FirstAcknowledgedAt = &at
			}
		}
	}
	return nil
}
