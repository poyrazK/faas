package state

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var ErrApplicationStandardLogHealthStale = errors.New("state: application standard logging health event superseded")

// Health is private current-process evidence. It neither establishes fleet
// membership nor advances observed_revision. Events contain no logs or errors.
type ApplicationStandardLogHealthEvent struct {
	EventRevision    int64  `json:"event_revision"`
	Status           string `json:"status"`
	Reason           string `json:"reason"`
	SourceInstanceID string `json:"source_instance_id"`
	Sequence         int64  `json:"sequence"`
}

type ApplicationStandardLogHealthObservation struct {
	ApplicationStandardLogDrainBinding
	ApplicationStandardLogConsumerSession
	ApplicationStandardLogHealthEvent
	EventAt    time.Time `json:"event_at"`
	ObservedAt time.Time `json:"observed_at"`
}

type ApplicationStandardLogHealthStore interface {
	RecordApplicationStandardLogHealth(context.Context, ApplicationStandardLogConsumerSession, AppLogDrain, ApplicationStandardLogHealthEvent) (ApplicationStandardLogHealthObservation, error)
	ListApplicationStandardLogHealth(context.Context, string, string) ([]ApplicationStandardLogHealthObservation, error)
}

func validStandardLogHealth(s ApplicationStandardLogConsumerSession, d AppLogDrain, e ApplicationStandardLogHealthEvent) bool {
	if !validStandardLogSession(s) || e.EventRevision < 1 || e.EventRevision > api.ApplicationStandardMaxLogHealthEvent || d.StandardBinding == nil || !d.Enabled {
		return false
	}
	b := d.StandardBinding
	if e.EventRevision == api.ApplicationStandardMaxLogHealthEvent && (e.Status != "degraded" || e.Reason != "reporter_exhausted") {
		return false
	}
	if !validStandardResourceRead(b.OrgID, b.AppID) || !validStandardResourceRead(b.DrainID, b.ResourceID) || !sameStandardUUID(b.AppID, d.AppID) || !sameStandardUUID(b.DrainID, d.ID) || b.DesiredRevision < 1 || b.DesiredRevision > api.ApplicationStandardMaxVersion {
		return false
	}
	if !standardLogInventoryHashValid(b.EffectiveHash) || !standardLogInventoryHashValid(b.ResourceConfigHash) || b.DrainConfigHash != ApplicationStandardLogDrainConfigHash(d) {
		return false
	}
	if e.Status == "healthy" {
		return e.Reason == "delivered" && validStandardLogDelivery(d, e.SourceInstanceID, uint64(e.Sequence))
	}
	if e.SourceInstanceID != "" || e.Sequence != 0 {
		return false
	}
	return e.Status == "unknown" && e.Reason == "idle" || e.Status == "degraded" && validStandardLogHealthFailure(e.Reason)
}

func validStandardLogHealthFailure(reason string) bool {
	switch reason {
	case "retrying", "delivery_failed", "queue_fault", "records_lost", "source_gap", "stream_unavailable", "reporter_exhausted":
		return true
	}
	return false
}

func canonicalStandardLogHealthSession(s ApplicationStandardLogConsumerSession) ApplicationStandardLogConsumerSession {
	s.NodeID, s.SessionID = canonicalStandardUUID(s.NodeID), canonicalStandardUUID(s.SessionID)
	return s
}

func standardLogHealthKey(app, drain, node string) string {
	return canonicalStandardUUID(app) + "\x00" + canonicalStandardUUID(drain) + "\x00" + canonicalStandardUUID(node)
}
