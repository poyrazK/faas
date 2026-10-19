package internalmail

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const RelayHealthPath = APIPath + "/relay/health"
const RelayOperationsPath = OperationsPath + "/relay"

var ErrRelayHeld = errors.New("internal mail relay backpressure is holding delivery")

type RelayObservation struct {
	ID            string    `json:"id"`
	ObservedAt    time.Time `json:"observed_at"`
	InventoryOK   bool      `json:"inventory_ok"`
	SMTPUp        bool      `json:"smtp_up"`
	QueueMessages int64     `json:"queue_messages"`
	OldestSeconds int64     `json:"oldest_seconds"`
}

func (o RelayObservation) normalized() (RelayObservation, error) {
	if !canonicalOperatorID(o.ID) || o.ObservedAt.IsZero() || o.ObservedAt.Year() < 1 || o.ObservedAt.Year() > 9999 ||
		o.QueueMessages < 0 || o.QueueMessages > api.InternalMailRelayMaxObservedMessages ||
		o.OldestSeconds < 0 || o.OldestSeconds > int64(api.InternalMailRelayMaxObservedAge/time.Second) ||
		o.QueueMessages == 0 && o.OldestSeconds != 0 || !o.InventoryOK && (o.QueueMessages != 0 || o.OldestSeconds != 0) {
		return o, ErrInvalid
	}
	o.ObservedAt = o.ObservedAt.UTC().Truncate(time.Microsecond)
	return o, nil
}

func sameRelayObservation(a, b RelayObservation) bool {
	return a.ID == b.ID && a.ObservedAt.Equal(b.ObservedAt) && a.InventoryOK == b.InventoryOK &&
		a.SMTPUp == b.SMTPUp && a.QueueMessages == b.QueueMessages && a.OldestSeconds == b.OldestSeconds
}

type RelayStatus struct {
	State        string            `json:"state"`
	Reason       string            `json:"reason"`
	Revision     int64             `json:"revision"`
	Held         bool              `json:"held"`
	Fresh        bool              `json:"fresh"`
	Observation  *RelayObservation `json:"observation,omitempty"`
	ReceivedAt   *time.Time        `json:"received_at,omitempty"`
	HealthySince *time.Time        `json:"healthy_since,omitempty"`
	UpdatedAt    time.Time         `json:"updated_at"`
}

type RelayEvent struct {
	ID            string    `json:"id"`
	Revision      int64     `json:"revision"`
	State         string    `json:"state"`
	Reason        string    `json:"reason"`
	ObservationID string    `json:"observation_id,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

func relayFresh(at, received, now time.Time) bool {
	return at.After(now.Add(-api.InternalMailRelayMaxSampleAge)) && received.After(now.Add(-api.InternalMailRelayMaxSampleAge)) &&
		!at.After(now.Add(api.InternalMailRelayClockSkew)) && !received.After(now.Add(api.InternalMailRelayClockSkew))
}

func relayObservationReason(o RelayObservation) string {
	switch {
	case !o.InventoryOK:
		return "inventory_unavailable"
	case !o.SMTPUp:
		return "smtp_unavailable"
	case o.QueueMessages >= api.InternalMailRelayQueueHighWater:
		return "queue_depth"
	case o.OldestSeconds >= int64(api.InternalMailRelayQueueMaxAge/time.Second):
		return "queue_age"
	default:
		return "healthy"
	}
}

func relayReasonAt(o RelayObservation, now time.Time) string {
	reason := relayObservationReason(o)
	if reason == "healthy" && o.QueueMessages > 0 && time.Duration(o.OldestSeconds)*time.Second+max(0, now.Sub(o.ObservedAt)) >= api.InternalMailRelayQueueMaxAge {
		return "queue_age"
	}
	return reason
}

func (s RelayStatus) view(now time.Time) RelayStatus {
	s.Fresh = s.Observation != nil && s.ReceivedAt != nil && relayFresh(s.Observation.ObservedAt, *s.ReceivedAt, now)
	if s.Observation == nil {
		s.State, s.Reason, s.HealthySince = "paused", "unobserved", nil
	} else if !s.Fresh {
		s.State, s.Reason, s.HealthySince = "paused", "stale", nil
	} else if reason := relayReasonAt(*s.Observation, now); reason != "healthy" {
		s.State, s.Reason, s.HealthySince = "paused", reason, nil
	}
	s.Held = s.State != "healthy"
	return s
}

func nextRelayObservation(previous RelayStatus, o RelayObservation, now time.Time) RelayStatus {
	previous = previous.view(now)
	next := previous
	next.Observation, next.ReceivedAt, next.UpdatedAt = &o, &now, now
	next.State, next.Reason, next.HealthySince = "paused", relayReasonAt(o, now), nil
	if next.Reason == "healthy" {
		next.HealthySince = previous.HealthySince
		if next.HealthySince == nil {
			next.HealthySince = &now
		}
		next.State, next.Reason = "recovering", "recovering"
		if now.Sub(*next.HealthySince) >= api.InternalMailRelayRecoveryWindow {
			next.State, next.Reason = "healthy", "healthy"
		}
	}
	return next.view(now)
}

type backpressureStore interface {
	observeRelay(context.Context, RelayObservation, func() bool) (RelayStatus, error)
	refreshRelay(context.Context) (RelayStatus, error)
	relayStatus(context.Context) (RelayStatus, error)
	relayEvents(context.Context, OperatorList) (OperatorPage[RelayEvent], error)
	claimWithRelay(context.Context, domainAuthorization, []string, bool, PacingPolicy, bool) (Recipient, error)
	beginWithRelay(context.Context, Recipient, string, domainAuthorization, []string, bool, PacingPolicy, bool) error
}

func (s *Service) backpressureEnabled() bool {
	return s.enabled() && s.BackpressureEnabled != nil && s.BackpressureEnabled()
}

func (s *Service) ReportRelayHealth(ctx context.Context, observation RelayObservation) (RelayStatus, error) {
	if !s.backpressureEnabled() || !s.reportsEnabled() {
		return RelayStatus{}, ErrDisabled
	}
	store, ok := s.Store.(backpressureStore)
	if !ok {
		return RelayStatus{}, ErrInvalid
	}
	return store.observeRelay(ctx, observation, func() bool { return s.backpressureEnabled() && s.reportsEnabled() })
}

func (s *Service) RelayStatus(ctx context.Context) (RelayStatus, error) {
	if !s.backpressureEnabled() || !s.operationsEnabled() {
		return RelayStatus{}, ErrDisabled
	}
	store, ok := s.Store.(backpressureStore)
	if !ok {
		return RelayStatus{}, ErrInvalid
	}
	return store.relayStatus(ctx)
}
