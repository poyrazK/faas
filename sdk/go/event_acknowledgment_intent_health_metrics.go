package faas

import (
	"sync"
	"time"
)

// EventAcknowledgmentIntentHealthMetricsOptions bounds cached evidence freshness.
type EventAcknowledgmentIntentHealthMetricsOptions struct {
	MaxAge time.Duration // Whole milliseconds 100..300000; zero defaults to 30 seconds.
}

// EventAcknowledgmentIntentHealthMetricsSample contains bounded metadata only.
// Incomplete sample counts are lower bounds, not proof of an empty backlog.
type EventAcknowledgmentIntentHealthMetricsSample struct {
	ObservedAt            time.Time `json:"observed_at"`
	ObservationAgeSeconds float64   `json:"observation_age_seconds"`
	Complete              bool      `json:"complete"`
	Truncated             bool      `json:"truncated"`
	ScanLimit             int       `json:"scan_limit"`
	InvalidRows           int64     `json:"invalid_rows"`
	Outstanding           int64     `json:"outstanding"`
	Due                   int64     `json:"due"`
	Stalled               int64     `json:"stalled"`
	Parked                int64     `json:"parked"`
}

// EventAcknowledgmentIntentHealthMetricsSnapshot owns its sample copy. Missing
// reads omit Health; they never manufacture zero-valued backlog evidence.
type EventAcknowledgmentIntentHealthMetricsSnapshot struct {
	ReadSuccess              bool                                          `json:"read_success"`
	EvidenceEligible         bool                                          `json:"evidence_eligible"`
	MaxObservationAgeSeconds float64                                       `json:"max_observation_age_seconds"`
	Health                   *EventAcknowledgmentIntentHealthMetricsSample `json:"health,omitempty"`
}

type eventIntentHealthMetricsRecorded struct {
	health     EventAcknowledgmentIntentHealthMetricsSample
	recordedAt time.Time // Includes this process's monotonic clock.
	age        time.Duration
	ageSeconds float64 // Retain full calendar range even when Duration saturates.
	eligible   bool
}

// EventAcknowledgmentIntentHealthMetrics is a fixed-memory cache for one owner
// scope. Construct with NewEventAcknowledgmentIntentHealthMetrics; do not copy it.
// Methods serialize concurrent polling/scrapes. No timer, SQL or listener starts
// on construction. Snapshots retain no owner, invocation, capability or error.
type EventAcknowledgmentIntentHealthMetrics struct {
	mu               sync.Mutex
	account, app     string
	maxAge           time.Duration
	previousObserved time.Time
	sample           *eventIntentHealthMetricsRecorded
	polling          bool
}

// NewEventAcknowledgmentIntentHealthMetrics validates scope and freshness without I/O.
func NewEventAcknowledgmentIntentHealthMetrics(accountID, appID string, options EventAcknowledgmentIntentHealthMetricsOptions) (*EventAcknowledgmentIntentHealthMetrics, error) {
	account, app, err := eventIntentScope(accountID, appID)
	if err != nil {
		return nil, err
	}
	if options.MaxAge == 0 {
		options.MaxAge = 30 * time.Second
	}
	if options.MaxAge < 100*time.Millisecond || options.MaxAge > 5*time.Minute || options.MaxAge%time.Millisecond != 0 {
		return nil, ErrInvalidEventConsumerRequest
	}
	return &EventAcknowledgmentIntentHealthMetrics{account: account, app: app, maxAge: options.MaxAge}, nil
}

func (m *EventAcknowledgmentIntentHealthMetrics) initialized() bool {
	return m != nil && m.maxAge != 0
}

func eventIntentHealthMetricsSampleValid(h EventAcknowledgmentIntentHealthMetricsSample) bool {
	if !eventIntentCheckpointTime(h.ObservedAt) || h.ScanLimit < 1 || h.ScanLimit > 10000 {
		return false
	}
	for _, n := range []int64{h.InvalidRows, h.Outstanding, h.Due, h.Stalled, h.Parked} {
		if n < 0 || n > int64(h.ScanLimit) {
			return false
		}
	}
	return h.Complete == (!h.Truncated && h.InvalidRows == 0) && h.InvalidRows <= h.Outstanding &&
		h.Due+h.Parked <= h.Outstanding && h.Stalled <= h.Due && (!h.Truncated || h.Outstanding == int64(h.ScanLimit))
}

func (m *EventAcknowledgmentIntentHealthMetrics) copyHealth(h EventAcknowledgmentIntentHealth) (EventAcknowledgmentIntentHealthMetricsSample, error) {
	sample := EventAcknowledgmentIntentHealthMetricsSample{
		ObservedAt: h.ObservedAt.UTC(), Complete: h.Complete, Truncated: h.Truncated,
		ScanLimit: h.ScanLimit, InvalidRows: h.InvalidRows, Outstanding: h.Outstanding,
		Due: h.Due, Stalled: h.Stalled, Parked: h.Parked,
	}
	account, app, err := eventIntentScope(h.AccountID, h.AppID)
	if err != nil || account != m.account || app != m.app || h.Coverage != "bounded_retained_unfinished_intents" || !eventIntentHealthMetricsSampleValid(sample) {
		return EventAcknowledgmentIntentHealthMetricsSample{}, ErrInvalidEventConsumerRequest
	}
	for _, n := range []int64{h.Pending, h.Unconfirmed, h.Leased, h.Scheduled, h.ExpiredClaims} {
		if n < 0 || n > int64(h.ScanLimit) {
			return EventAcknowledgmentIntentHealthMetricsSample{}, ErrInvalidEventConsumerRequest
		}
	}
	if h.Pending+h.Unconfirmed+h.Parked != h.Outstanding || h.Due+h.Leased+h.Scheduled+h.Parked != h.Outstanding || h.ExpiredClaims > h.Due+h.Scheduled {
		return EventAcknowledgmentIntentHealthMetricsSample{}, ErrInvalidEventConsumerRequest
	}
	return sample, nil
}

// Record copies one completed scoped read. Malformed input clears cached counts
// and returns a fixed error. Failed reads retain the database high-water mark.
// Feed manual reads sequentially, or use the explicit poller as the sole source.
func (m *EventAcknowledgmentIntentHealthMetrics) Record(health EventAcknowledgmentIntentHealth) error {
	if !m.initialized() {
		return ErrInvalidEventConsumerRequest
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	sample, err := m.copyHealth(health)
	if err != nil {
		m.sample = nil
		return err
	}
	now := time.Now()
	wall := now.UTC() // Strip monotonic time before comparing database/wall clocks.
	age := max(time.Duration(0), wall.Sub(sample.ObservedAt))
	fresh := !sample.ObservedAt.After(wall) && age <= m.maxAge
	advancing := m.previousObserved.IsZero() || sample.ObservedAt.After(m.previousObserved)
	m.sample = &eventIntentHealthMetricsRecorded{
		health: sample, recordedAt: now, age: age,
		ageSeconds: eventIntentHealthMetricsWallAge(wall, sample.ObservedAt),
		eligible:   sample.Complete && fresh && advancing,
	}
	// Fresh incomplete reads also advance the mark; time alone cannot repair them.
	if fresh && advancing {
		m.previousObserved = sample.ObservedAt
	}
	return nil
}

// RecordReadFailure clears cached evidence without retaining a raw storage error.
func (m *EventAcknowledgmentIntentHealthMetrics) RecordReadFailure() {
	if !m.initialized() {
		return
	}
	m.mu.Lock()
	m.sample = nil
	m.mu.Unlock()
}

// Snapshot recomputes freshness for every scrape, returning an independent copy.
// Wall-clock regression invalidates eligibility. Monotonic elapsed time prevents
// a backward clock adjustment from extending freshness. A nil/zero cache returns
// an invalid zero snapshot, which the formatter rejects.
func (m *EventAcknowledgmentIntentHealthMetrics) Snapshot() EventAcknowledgmentIntentHealthMetricsSnapshot {
	if !m.initialized() {
		return EventAcknowledgmentIntentHealthMetricsSnapshot{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := EventAcknowledgmentIntentHealthMetricsSnapshot{MaxObservationAgeSeconds: m.maxAge.Seconds()}
	saved := m.sample
	if saved == nil {
		return out
	}
	now := time.Now()
	elapsed := now.Sub(saved.recordedAt) // Monotonic while both times are local.
	monotonicAge := time.Duration(1<<63 - 1)
	if max(time.Duration(0), elapsed) <= monotonicAge-saved.age {
		monotonicAge = saved.age + max(time.Duration(0), elapsed)
	}
	age := max(time.Duration(0), now.UTC().Sub(saved.health.ObservedAt), monotonicAge)
	health := saved.health
	health.ObservationAgeSeconds = max(eventIntentHealthMetricsWallAge(now.UTC(), health.ObservedAt), saved.ageSeconds+max(time.Duration(0), elapsed).Seconds())
	out.ReadSuccess, out.Health = true, &health
	out.EvidenceEligible = saved.eligible && !now.UTC().Before(saved.recordedAt.UTC()) && elapsed >= 0 && age <= m.maxAge && health.ObservationAgeSeconds <= m.maxAge.Seconds()
	return out
}

func eventIntentHealthMetricsWallAge(wall, observed time.Time) float64 {
	// Valid observation years 1..9999 keep second subtraction within int64.
	// Avoid UnixNano overflow and Duration's roughly 292-year saturation.
	return max(0, float64(wall.Unix()-observed.Unix())+float64(wall.Nanosecond()-observed.Nanosecond())/1e9)
}

func (m *EventAcknowledgmentIntentHealthMetrics) beginPolling(account, app string) error {
	if !m.initialized() {
		return ErrInvalidEventConsumerRequest
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.polling || account != m.account || app != m.app {
		return ErrInvalidEventConsumerRequest
	}
	m.polling = true
	return nil
}

func (m *EventAcknowledgmentIntentHealthMetrics) endPolling() {
	m.mu.Lock()
	m.sample, m.polling = nil, false
	m.mu.Unlock()
}
