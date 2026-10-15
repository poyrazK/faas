package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// The upper revision is fixed on the first page. ExpectedEpisodeID fences
// a legacy alias from crossing into a recurrence on subsequent requests.
type OperatorAlertTriageHistoryFilter struct {
	ID, ExpectedEpisodeID string
	SnapshotRevision      *int64
	BeforeRevision        int64
	Limit                 int
}

type OperatorAlertTriageHistorySnapshot struct {
	Episode          OperatorAlertEpisode
	Collection       OperatorAlertCollection
	SnapshotRevision int64
	Items            []api.ObsAlertTriageHistoryEntry
	Coverage         api.ObsAlertTriageHistoryCoverage
	Truncated        bool
}

type OperatorAlertTriageHistoryReader interface {
	OperatorAlertTriageHistory(context.Context, OperatorAlertTriageHistoryFilter) (OperatorAlertTriageHistorySnapshot, error)
}

var errAlertTriageReceipt = errors.New("invalid operator alert triage receipt")

func validateOperatorAlertTriageHistoryFilter(f OperatorAlertTriageHistoryFilter) error {
	if !operatorAlertHash.MatchString(f.ID) || f.ExpectedEpisodeID != "" && !operatorAlertHash.MatchString(f.ExpectedEpisodeID) || f.Limit < 1 || f.Limit > api.ObsAlertTriageHistoryLimitMax || f.BeforeRevision < 0 {
		return ErrInvalidArgument
	}
	if f.SnapshotRevision == nil {
		if f.BeforeRevision != 0 || f.ExpectedEpisodeID != "" {
			return ErrInvalidArgument
		}
	} else if *f.SnapshotRevision < 0 || *f.SnapshotRevision > api.ObsAlertTriageRevisionMax || f.BeforeRevision > *f.SnapshotRevision {
		return ErrInvalidArgument
	}
	return nil
}

func triageHistoryRevision(e OperatorAlertEpisode, f OperatorAlertTriageHistoryFilter) (int64, error) {
	if e.Triage.Revision < 0 || e.Triage.Revision > api.ObsAlertTriageRevisionMax {
		return 0, errAlertTriageReceipt
	}
	if f.ExpectedEpisodeID != "" && f.ExpectedEpisodeID != e.ID {
		return 0, ErrInvalidArgument
	}
	upper := e.Triage.Revision
	if f.SnapshotRevision != nil {
		upper = *f.SnapshotRevision
		if upper > e.Triage.Revision {
			return 0, ErrInvalidArgument
		}
	}
	return upper, nil
}

func triageHistoryCoverage(upper, total, distinct int64, oldest, newest *int64) (api.ObsAlertTriageHistoryCoverage, error) {
	if total != distinct || distinct < 0 || distinct > upper {
		return api.ObsAlertTriageHistoryCoverage{}, errAlertTriageReceipt
	}
	c := api.ObsAlertTriageHistoryCoverage{State: "complete", AvailableEntries: distinct, MissingEntries: upper - distinct, OldestAvailableRevision: oldest, NewestAvailableRevision: newest}
	if c.MissingEntries > 0 {
		c.State = "gaps"
		c.Reason = "missing_receipts"
	}
	return c, nil
}

// Only this closed projection can leave the audit store. Even the Postgres
// query removes actor fields before decoding; memory mirrors that projection.
type operatorAlertTriageReceipt struct {
	EpisodeID        string `json:"episode_id"`
	Action           string `json:"action"`
	PreviousRevision *int64 `json:"previous_revision"`
	Triage           struct {
		Revision     *int64     `json:"revision"`
		Acknowledged *bool      `json:"acknowledged"`
		Owned        *bool      `json:"owned"`
		SnoozedUntil *time.Time `json:"snoozed_until"`
		AsOf         *time.Time `json:"as_of"`
	} `json:"triage"`
	TraceID string `json:"trace_id"`
}

func decodeAlertTriageReceipt(id, episode string, at time.Time, payload []byte) (api.ObsAlertTriageHistoryEntry, error) {
	var r operatorAlertTriageReceipt
	if err := json.Unmarshal(payload, &r); err != nil {
		return api.ObsAlertTriageHistoryEntry{}, errAlertTriageReceipt
	}
	parsed, err := uuid.Parse(id)
	if err != nil || parsed == uuid.Nil || r.EpisodeID != episode || !api.AllowedObsAlertTriageAction(r.Action) ||
		r.PreviousRevision == nil || r.Triage.Revision == nil || *r.Triage.Revision < 1 || *r.Triage.Revision > api.ObsAlertTriageRevisionMax ||
		*r.PreviousRevision != *r.Triage.Revision-1 || r.Triage.Acknowledged == nil || r.Triage.Owned == nil ||
		r.Triage.AsOf == nil || at.IsZero() || !r.Triage.AsOf.Equal(at) {
		return api.ObsAlertTriageHistoryEntry{}, errAlertTriageReceipt
	}
	if r.Action == "acknowledge" && !*r.Triage.Acknowledged || r.Action == "unacknowledge" && *r.Triage.Acknowledged ||
		r.Action == "claim" && !*r.Triage.Owned || r.Action == "release" && *r.Triage.Owned ||
		r.Action == "unsnooze" && r.Triage.SnoozedUntil != nil {
		return api.ObsAlertTriageHistoryEntry{}, errAlertTriageReceipt
	}
	if r.Action == "snooze" && (r.Triage.SnoozedUntil == nil || !r.Triage.SnoozedUntil.After(at) || r.Triage.SnoozedUntil.After(at.Add(api.ObsAlertTriageSnoozeMax))) {
		return api.ObsAlertTriageHistoryEntry{}, errAlertTriageReceipt
	}
	trace := ""
	if auditTraceIDPattern.MatchString(r.TraceID) {
		trace = r.TraceID
	}
	return api.ObsAlertTriageHistoryEntry{ID: parsed.String(), Revision: *r.Triage.Revision, Action: r.Action, At: at.UTC(), Acknowledged: *r.Triage.Acknowledged, Owned: *r.Triage.Owned, SnoozedUntil: r.Triage.SnoozedUntil, TraceID: trace}, nil
}
