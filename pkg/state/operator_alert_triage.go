package state

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type OperatorAlertTriage struct {
	Revision                int64
	Acknowledged            bool
	OwnerID                 string
	SnoozedUntil, UpdatedAt *time.Time
}

type OperatorAlertTriageMutation struct {
	EpisodeID, ActorID, Action string
	ExpectedRevision           int64
	SnoozedUntil               *time.Time
	Now                        time.Time
	TraceID                    *string
}

type OperatorAlertTriageWriter interface {
	MutateOperatorAlertTriage(context.Context, OperatorAlertTriageMutation) (OperatorAlertTriage, error)
}

func (t OperatorAlertTriage) Projection(now time.Time) api.ObsAlertTriage {
	t = copyOperatorAlertTriage(t)
	return api.ObsAlertTriage{Revision: t.Revision, Acknowledged: t.Acknowledged, Owned: t.OwnerID != "", OwnerAccountID: t.OwnerID,
		SnoozedUntil: t.SnoozedUntil, Snoozed: t.SnoozedUntil != nil && t.SnoozedUntil.After(now), UpdatedAt: t.UpdatedAt, AsOf: now}
}

func normalizeOperatorAlertTriageMutation(m OperatorAlertTriageMutation) (OperatorAlertTriageMutation, error) {
	actor, err := uuid.Parse(m.ActorID)
	if err != nil || actor == uuid.Nil || !operatorAlertHash.MatchString(m.EpisodeID) || m.Now.IsZero() || m.ExpectedRevision < 0 || m.ExpectedRevision >= api.ObsAlertTriageRevisionMax || !api.AllowedObsAlertTriageAction(m.Action) {
		return m, ErrInvalidArgument
	}
	m.ActorID = actor.String()
	m.Now = m.Now.UTC().Truncate(time.Microsecond)
	if m.TraceID != nil && !auditTraceIDPattern.MatchString(*m.TraceID) {
		return m, ErrInvalidArgument
	}
	if m.Action == "snooze" {
		if m.SnoozedUntil == nil {
			return m, ErrInvalidArgument
		}
		until := m.SnoozedUntil.UTC().Truncate(time.Microsecond)
		if !until.After(m.Now) || until.After(m.Now.Add(api.ObsAlertTriageSnoozeMax)) {
			return m, ErrInvalidArgument
		}
		m.SnoozedUntil = &until
	} else if m.SnoozedUntil != nil {
		return m, ErrInvalidArgument
	}
	return m, nil
}

func applyOperatorAlertTriage(t OperatorAlertTriage, m OperatorAlertTriageMutation) (OperatorAlertTriage, error) {
	if t.Revision != m.ExpectedRevision {
		return t, ErrConflict
	}
	t = copyOperatorAlertTriage(t)
	switch m.Action {
	case "acknowledge":
		t.Acknowledged = true
	case "unacknowledge":
		t.Acknowledged = false
	case "claim":
		if t.OwnerID != "" && t.OwnerID != m.ActorID {
			return t, ErrConflict
		}
		t.OwnerID = m.ActorID
	case "release":
		if t.OwnerID != "" && t.OwnerID != m.ActorID {
			return t, ErrConflict
		}
		t.OwnerID = ""
	case "snooze":
		t.SnoozedUntil = m.SnoozedUntil
	case "unsnooze":
		t.SnoozedUntil = nil
	}
	t.Revision++
	t.UpdatedAt = &m.Now
	return t, nil
}

func operatorAlertTriageAudit(m OperatorAlertTriageMutation, t OperatorAlertTriage) (AuditLog, error) {
	actor, err := uuid.Parse(m.ActorID)
	if err != nil {
		return AuditLog{}, err
	}
	data, err := json.Marshal(struct {
		EpisodeID        string             `json:"episode_id"`
		Action           string             `json:"action"`
		PreviousRevision int64              `json:"previous_revision"`
		Triage           api.ObsAlertTriage `json:"triage"`
	}{m.EpisodeID, m.Action, m.ExpectedRevision, t.Projection(m.Now)})
	if err != nil {
		return AuditLog{}, err
	}
	entry := AuditLog{ID: uuid.New(), Kind: "operator.action.alert_triage", AccountID: &actor, Actor: m.ActorID, ReceivedAt: m.Now, TraceID: m.TraceID, Data: data}
	entry.Data, err = auditLogDataWithTrace(entry)
	return entry, err
}

func copyOperatorAlertTriage(t OperatorAlertTriage) OperatorAlertTriage {
	if t.SnoozedUntil != nil {
		at := *t.SnoozedUntil
		t.SnoozedUntil = &at
	}
	if t.UpdatedAt != nil {
		at := *t.UpdatedAt
		t.UpdatedAt = &at
	}
	return t
}

// Filters are applied before pagination in both stores. An expired snooze is
// inactive even if its original deadline remains available as evidence.
func matchOperatorAlertTriage(t OperatorAlertTriage, f OperatorAlertFilter) bool {
	snoozed := t.SnoozedUntil != nil && t.SnoozedUntil.After(f.Now)
	acknowledged := f.Acknowledgment == "" || t.Acknowledged == (f.Acknowledgment == "acknowledged")
	owned := f.Ownership == "" || f.Ownership == "owned" && t.OwnerID != "" ||
		f.Ownership == "unowned" && t.OwnerID == "" || f.Ownership == "mine" && t.OwnerID == f.OwnerID
	snooze := f.Snooze == "" || snoozed == (f.Snooze == "active")
	return acknowledged && owned && snooze
}
