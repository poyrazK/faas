package preview

import (
	"context"
	"database/sql"
	"sync"
	"time"
)

// EventAcknowledgmentIntentRecoveryWorkerMetricsOptions declares scheduling
// expectations, not deadlines or permission to start recovery work.
type EventAcknowledgmentIntentRecoveryWorkerMetricsOptions struct {
	MaxIdle         time.Duration // Whole milliseconds 100..86400000; zero defaults to one minute.
	MaxPassDuration time.Duration // Whole milliseconds 1000..86400000; zero defaults to five minutes.
}

// EventAcknowledgmentIntentRecoveryWorkerPassSample describes the latest joined
// pass. Result is absent if returned progress was malformed; timestamps/duration
// still describe activity. Counts are per-pass gauges, not cumulative counters.
type EventAcknowledgmentIntentRecoveryWorkerPassSample struct {
	StartedAt            time.Time                                    `json:"started_at"`
	CompletedAt          time.Time                                    `json:"completed_at"`
	DurationSeconds      float64                                      `json:"duration_seconds"`
	CompletionAgeSeconds float64                                      `json:"completion_age_seconds"`
	Result               *EventAcknowledgmentIntentRecoveryPassResult `json:"result,omitempty"`
}

// EventAcknowledgmentIntentRecoveryWorkerMetricsSnapshot owns all returned
// copies. Before the first pass, activity refers to monitoring construction;
// no pass result is fabricated. ClockValid=false means unknown liveness.
type EventAcknowledgmentIntentRecoveryWorkerMetricsSnapshot struct {
	ClockValid         bool                                               `json:"clock_valid"`
	InFlight           bool                                               `json:"in_flight"`
	Overdue            bool                                               `json:"overdue"`
	MaxIdleSeconds     float64                                            `json:"max_idle_seconds"`
	MaxPassSeconds     float64                                            `json:"max_pass_seconds"`
	ActivityAt         time.Time                                          `json:"activity_at"`
	ActivityAgeSeconds float64                                            `json:"activity_age_seconds"`
	LastStartedAt      *time.Time                                         `json:"last_started_at,omitempty"`
	LastPass           *EventAcknowledgmentIntentRecoveryWorkerPassSample `json:"last_pass,omitempty"`
}

type eventIntentRecoveryWorkerPass struct {
	started, completed time.Time // Keep local monotonic readings until snapshotting.
	result             *EventAcknowledgmentIntentRecoveryPassResult
}

// EventAcknowledgmentIntentRecoveryWorkerMetrics is a fixed-memory, concurrency-
// safe cache for one owner scope and one nonoverlapping recovery-pass stream.
// Use its constructor and retain a pointer; the zero value is invalid. Never copy
// it. Scrapes and health reads cannot refresh worker activity. No I/O starts here.
type EventAcknowledgmentIntentRecoveryWorkerMetrics struct {
	mu                   sync.Mutex
	account, app         string
	maxIdle, maxPass     time.Duration
	enabled, activity    time.Time
	lastStarted          time.Time
	lastPass             *eventIntentRecoveryWorkerPass
	inFlight, clockValid bool
}

// NewEventAcknowledgmentIntentRecoveryWorkerMetrics begins local scheduling
// observation. Schedule the first explicit pass within MaxIdle of construction.
func NewEventAcknowledgmentIntentRecoveryWorkerMetrics(accountID, appID string, options EventAcknowledgmentIntentRecoveryWorkerMetricsOptions) (*EventAcknowledgmentIntentRecoveryWorkerMetrics, error) {
	account, app, err := eventIntentScope(accountID, appID)
	if err != nil {
		return nil, err
	}
	if options.MaxIdle == 0 {
		options.MaxIdle = time.Minute
	}
	if options.MaxPassDuration == 0 {
		options.MaxPassDuration = 5 * time.Minute
	}
	if options.MaxIdle < 100*time.Millisecond || options.MaxIdle > 24*time.Hour || options.MaxIdle%time.Millisecond != 0 || options.MaxPassDuration < time.Second || options.MaxPassDuration > 24*time.Hour || options.MaxPassDuration%time.Millisecond != 0 {
		return nil, ErrInvalidEventConsumerRequest
	}
	now := time.Now()
	return &EventAcknowledgmentIntentRecoveryWorkerMetrics{
		account: account, app: app, maxIdle: options.MaxIdle, maxPass: options.MaxPassDuration,
		enabled: now, activity: now, clockValid: true,
	}, nil
}

func (m *EventAcknowledgmentIntentRecoveryWorkerMetrics) initialized() bool {
	return m != nil && m.maxIdle != 0
}

func eventIntentRecoveryWorkerClock(now, event time.Time) bool {
	return !now.UTC().Before(event.UTC()) && now.Sub(event) >= 0
}

func eventIntentRecoveryWorkerAge(now, event time.Time) float64 {
	return max(eventIntentHealthMetricsWallAge(now.UTC(), event.UTC()), max(time.Duration(0), now.Sub(event)).Seconds())
}

func (m *EventAcknowledgmentIntentRecoveryWorkerMetrics) beginPass(account, app string) error {
	if !m.initialized() {
		return ErrInvalidEventConsumerRequest
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.inFlight || m.account != account || m.app != app {
		return ErrInvalidEventConsumerRequest
	}
	now := time.Now()
	m.clockValid = m.clockValid && eventIntentRecoveryWorkerClock(now, m.activity) && eventIntentRecoveryWorkerClock(now, m.enabled)
	m.activity, m.lastStarted, m.inFlight = now, now, true
	return nil
}

func eventIntentRecoveryWorkerResultValid(r EventAcknowledgmentIntentRecoveryPassResult) bool {
	if r.Attempts < 0 || r.Attempts > 10000 {
		return false
	}
	total := 0
	for _, n := range []int{r.Confirmed, r.Unconfirmed, r.Parked, r.Superseded, r.Empty, r.ClaimLost, r.Interrupted, r.Failed, r.CommitUnknown} {
		if n < 0 || n > r.Attempts {
			return false
		}
		total += n
	}
	if total != r.Attempts {
		return false
	}
	if r.CommitUnknown > 0 {
		return r.StopReason == "commit_unknown"
	}
	switch r.StopReason {
	case "interrupted":
		return true // Caller cancellation may happen after all known settlements.
	case "failed":
		return r.Failed > 0 && r.Interrupted == 0
	case "empty":
		return r.Empty > 0 && r.Failed == 0 && r.Interrupted == 0
	case "limit":
		return r.Attempts > 0 && r.Empty == 0 && r.Failed == 0 && r.Interrupted == 0
	default:
		return false
	}
}

func (m *EventAcknowledgmentIntentRecoveryWorkerMetrics) finishPass(result EventAcknowledgmentIntentRecoveryPassResult) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	m.clockValid = m.clockValid && eventIntentRecoveryWorkerClock(now, m.activity) && eventIntentRecoveryWorkerClock(now, m.enabled)
	m.lastPass = &eventIntentRecoveryWorkerPass{started: m.lastStarted, completed: now}
	if eventIntentRecoveryWorkerResultValid(result) {
		m.lastPass.result = &result
	}
	m.activity, m.inFlight = now, false
}

// Snapshot recomputes conservative ages without refreshing activity. Detected
// clock regression remains invalid for this cache's lifetime. It never resolves
// itself because time passes. Replace the cache only after reviewing the clock.
func (m *EventAcknowledgmentIntentRecoveryWorkerMetrics) Snapshot() EventAcknowledgmentIntentRecoveryWorkerMetricsSnapshot {
	if !m.initialized() {
		return EventAcknowledgmentIntentRecoveryWorkerMetricsSnapshot{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	m.clockValid = m.clockValid && eventIntentRecoveryWorkerClock(now, m.activity) && eventIntentRecoveryWorkerClock(now, m.enabled)
	out := EventAcknowledgmentIntentRecoveryWorkerMetricsSnapshot{
		ClockValid: m.clockValid, InFlight: m.inFlight, MaxIdleSeconds: m.maxIdle.Seconds(), MaxPassSeconds: m.maxPass.Seconds(),
		ActivityAt: m.activity.UTC(), ActivityAgeSeconds: eventIntentRecoveryWorkerAge(now, m.activity),
		LastStartedAt: cloneEventIntentTime(&m.lastStarted),
	}
	if m.lastStarted.IsZero() {
		out.LastStartedAt = nil
	}
	bound := out.MaxIdleSeconds
	if out.InFlight {
		bound = out.MaxPassSeconds
	}
	out.Overdue = out.ClockValid && out.ActivityAgeSeconds > bound
	if saved := m.lastPass; saved != nil {
		out.LastPass = &EventAcknowledgmentIntentRecoveryWorkerPassSample{
			StartedAt: saved.started.UTC(), CompletedAt: saved.completed.UTC(),
			DurationSeconds:      max(time.Duration(0), saved.completed.Sub(saved.started)).Seconds(),
			CompletionAgeSeconds: eventIntentRecoveryWorkerAge(now, saved.completed),
		}
		if saved.result != nil {
			copy := *saved.result
			out.LastPass.Result = &copy
		}
	}
	return out
}

// RecoverEventAcknowledgmentIntentsWithMetrics records actual start/joined return
// around the existing bounded recovery pass, preserving its progress and error.
// All validation, including matching scope and sole cache ownership, precedes
// recovery I/O. It starts no scheduler, timer, listener or extra goroutine.
// A panic leaves the pass in flight; do not manufacture completion or overlap it.
func RecoverEventAcknowledgmentIntentsWithMetrics(ctx context.Context, db *sql.DB, client *Client, accountID, appID string, options EventAcknowledgmentIntentRecoveryPassOptions, metrics *EventAcknowledgmentIntentRecoveryWorkerMetrics) (EventAcknowledgmentIntentRecoveryPassResult, error) {
	var zero EventAcknowledgmentIntentRecoveryPassResult
	if ctx == nil || db == nil || client == nil || client.Client == nil {
		return zero, ErrInvalidEventConsumerRequest
	}
	account, app, err := eventIntentScope(accountID, appID)
	if err != nil {
		return zero, err
	}
	options, err = eventIntentRecoveryPassOptions(options)
	if err != nil {
		return zero, err
	}
	if !metrics.initialized() {
		return zero, ErrInvalidEventConsumerRequest
	}
	if err := ctx.Err(); err != nil {
		zero.StopReason = "interrupted"
		return zero, err
	}
	if err := metrics.beginPass(account, app); err != nil {
		return zero, err
	}
	result, err := RecoverEventAcknowledgmentIntents(ctx, db, client, account, app, options)
	metrics.finishPass(result) // The underlying pass has joined every admitted worker.
	return result, err
}
