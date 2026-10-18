package faas

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var (
	ErrEventAcknowledgmentIntentCheckpointUnavailable    = errors.New("acknowledgment intent checkpoint session unavailable")
	ErrEventAcknowledgmentIntentCheckpointCallbackFailed = errors.New("checkpointed recovery watch callback failed; reconcile returned checkpoint before notification retry")
)

// EventAcknowledgmentIntentCheckpointWatchOptions selects explicit durable
// monitoring. Callbacks run synchronously with detached snapshots. Persist the
// complete checkpoint atomically before any notification; fence one writer.
type EventAcknowledgmentIntentCheckpointWatchOptions struct {
	EventAcknowledgmentIntentWatchOptions
	Resume              *EventAcknowledgmentIntentCheckpoint
	OnProgress          func(context.Context, EventAcknowledgmentIntentCheckpointWatchResult) error
	PollAcknowledgments func(context.Context, EventAcknowledgmentIntentCheckpoint) ([]EventAcknowledgmentIntentAcknowledgment, error)
}

type EventAcknowledgmentIntentCheckpointWatchResult struct {
	StopReason     string                                  `json:"stop_reason"`
	Observations   int64                                   `json:"observations"`
	EvidenceStatus string                                  `json:"evidence_status"`
	EvidenceReason string                                  `json:"evidence_reason"`
	Checkpoint     EventAcknowledgmentIntentCheckpoint     `json:"checkpoint"`
	Health         *EventAcknowledgmentIntentHealth        `json:"health,omitempty"`
	Alerts         []EventAcknowledgmentIntentNotification `json:"alerts"`
	Confirmations  []EventAcknowledgmentIntentConfirmation `json:"confirmations"`
}

// EventAcknowledgmentIntentCheckpointWatchCallbackError exposes only the fixed
// callback phase (progress or acknowledgments), never an application cause.
// The detached checkpoint is in the watch's returned result, including on error.
type EventAcknowledgmentIntentCheckpointWatchCallbackError struct {
	Phase string `json:"phase"`
}

func (e *EventAcknowledgmentIntentCheckpointWatchCallbackError) Error() string {
	return ErrEventAcknowledgmentIntentCheckpointCallbackFailed.Error()
}
func (e *EventAcknowledgmentIntentCheckpointWatchCallbackError) Unwrap() error {
	return ErrEventAcknowledgmentIntentCheckpointCallbackFailed
}

func newEventIntentCheckpoint(account, app string, policy EventAcknowledgmentIntentCheckpointPolicy) (EventAcknowledgmentIntentCheckpoint, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return EventAcknowledgmentIntentCheckpoint{}, ErrEventAcknowledgmentIntentCheckpointUnavailable
	}
	id[6], id[8] = id[6]&0x0f|0x40, id[8]&0x3f|0x80
	return EventAcknowledgmentIntentCheckpoint{
		Kind: EventAcknowledgmentIntentCheckpointKind, Version: eventIntentCheckpointVersion(policy), AccountID: account, AppID: app, Policy: policy,
		Session:    fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:]),
		Conditions: eventIntentCheckpointRules(policy), Pending: []EventAcknowledgmentIntentNotification{}, Acknowledged: []EventAcknowledgmentIntentNotification{},
	}, nil
}

// WatchEventAcknowledgmentIntentHealthWithCheckpoint preserves incident identity
// and pending lifecycle notifications across host-persisted checkpoints. It reads
// local health only; storage, fencing and notification delivery belong to the host.
// It returns joined final state with context.Canceled/DeadlineExceeded on shutdown.
// Invalid input returns zero progress before callbacks or health reads.
func WatchEventAcknowledgmentIntentHealthWithCheckpoint(ctx context.Context, db *sql.DB, accountID, appID string, options EventAcknowledgmentIntentCheckpointWatchOptions) (EventAcknowledgmentIntentCheckpointWatchResult, error) {
	var zero EventAcknowledgmentIntentCheckpointWatchResult
	if ctx == nil || db == nil || options.OnProgress == nil {
		return zero, ErrInvalidEventConsumerRequest
	}
	account, app, err := eventIntentScope(accountID, appID)
	if err != nil {
		return zero, err
	}
	settings, err := eventIntentWatchOptions(options.EventAcknowledgmentIntentWatchOptions)
	if err != nil {
		return zero, err
	}
	var checkpoint EventAcknowledgmentIntentCheckpoint
	if options.Resume == nil {
		checkpoint, err = newEventIntentCheckpoint(account, app, eventIntentCheckpointPolicy(settings))
	} else {
		checkpoint, err = ValidateEventAcknowledgmentIntentCheckpoint(*options.Resume)
	}
	if err != nil {
		return zero, err
	}
	if checkpoint.AccountID != account || checkpoint.AppID != app || checkpoint.Policy != eventIntentCheckpointPolicy(settings) {
		return zero, ErrInvalidEventAcknowledgmentIntentCheckpoint
	}
	s := eventIntentCheckpointWatch{ctx: ctx, db: db, settings: settings, progress: options.OnProgress, acknowledgments: options.PollAcknowledgments}
	s.result = EventAcknowledgmentIntentCheckpointWatchResult{StopReason: "watching", EvidenceStatus: "unknown", EvidenceReason: "awaiting_observation", Checkpoint: checkpoint, Alerts: []EventAcknowledgmentIntentNotification{}}
	s.reset("fresh_confirmation_required")
	err = s.run()
	if ctx.Err() != nil {
		s.result.StopReason, s.result.EvidenceStatus, s.result.EvidenceReason = "interrupted", "unknown", "interrupted"
		s.result.Alerts = []EventAcknowledgmentIntentNotification{}
		s.reset("interrupted")
		err = ctx.Err()
	}
	return cloneEventIntentCheckpointWatchResult(s.result), err
}

type eventIntentCheckpointWatch struct {
	ctx             context.Context
	db              *sql.DB
	settings        EventAcknowledgmentIntentWatchOptions
	progress        func(context.Context, EventAcknowledgmentIntentCheckpointWatchResult) error
	acknowledgments func(context.Context, EventAcknowledgmentIntentCheckpoint) ([]EventAcknowledgmentIntentAcknowledgment, error)
	proofs          []eventIntentCheckpointProof
	result          EventAcknowledgmentIntentCheckpointWatchResult
}

func (s *eventIntentCheckpointWatch) reset(reason string) {
	s.proofs = resetEventIntentCheckpointProofs(&s.result.Checkpoint)
	s.result.Confirmations = make([]EventAcknowledgmentIntentConfirmation, len(s.proofs))
	for i, p := range s.proofs {
		s.result.Confirmations[i] = eventIntentCheckpointConfirmation(s.result.Checkpoint.Conditions[i], p, time.Time{}, s.settings.Sustain, reason, false)
	}
}

func cloneEventIntentCheckpointWatchResult(r EventAcknowledgmentIntentCheckpointWatchResult) EventAcknowledgmentIntentCheckpointWatchResult {
	r.Checkpoint = cloneEventIntentCheckpoint(r.Checkpoint)
	r.Alerts = append([]EventAcknowledgmentIntentNotification{}, r.Alerts...)
	r.Confirmations = append([]EventAcknowledgmentIntentConfirmation{}, r.Confirmations...)
	if r.Health != nil {
		h := *r.Health
		h.ObservedAt = h.ObservedAt.UTC()
		h.OldestOutstandingAt, h.OldestEligibleAt = cloneEventIntentTime(h.OldestOutstandingAt), cloneEventIntentTime(h.OldestEligibleAt)
		r.Health = &h
	}
	return r
}

func (s *eventIntentCheckpointWatch) callbackFailure(phase string) error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	s.result.StopReason = "callback_failure"
	return &EventAcknowledgmentIntentCheckpointWatchCallbackError{Phase: phase}
}

func (s *eventIntentCheckpointWatch) publish() error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	if err := s.progress(s.ctx, cloneEventIntentCheckpointWatchResult(s.result)); err != nil {
		return s.callbackFailure("progress")
	}
	return s.ctx.Err()
}

func (s *eventIntentCheckpointWatch) acknowledge() error {
	if err := s.ctx.Err(); err != nil || s.acknowledgments == nil {
		return err
	}
	acks, err := s.acknowledgments(s.ctx, cloneEventIntentCheckpoint(s.result.Checkpoint))
	if err != nil {
		return s.callbackFailure("acknowledgments")
	}
	if err := s.ctx.Err(); err != nil {
		return err
	}
	checkpoint, err := AcknowledgeEventAcknowledgmentIntentAlerts(s.result.Checkpoint, acks)
	if err != nil {
		return s.callbackFailure("acknowledgments")
	}
	s.result.Checkpoint = checkpoint
	return nil
}

func (s *eventIntentCheckpointWatch) run() error {
	if err := s.acknowledge(); err != nil {
		return err
	}
	if err := s.publish(); err != nil { // Persist/replay before the first health read.
		return err
	}
	var previousObserved, previousPoll time.Time
	if s.result.Checkpoint.LastObservedAt != nil {
		previousObserved = *s.result.Checkpoint.LastObservedAt
	}
	for {
		if err := s.ctx.Err(); err != nil {
			return err
		}
		callCtx, cancel := context.WithTimeout(s.ctx, s.settings.QueryTimeout)
		health, readErr := GetEventAcknowledgmentIntentHealth(callCtx, s.db, s.result.Checkpoint.AccountID, s.result.Checkpoint.AppID, s.settings.Health)
		cancel()
		if err := s.ctx.Err(); err != nil {
			return err
		}
		now := time.Now() // Monotonic clock remains local; only UTC wall times persist.
		reason := eventIntentWatchEvidence(health, readErr, previousObserved, previousPoll, now, s.settings.MaxAge)
		previousPoll = now
		if readErr == nil && !health.ObservedAt.IsZero() && !health.ObservedAt.After(now) && now.Sub(health.ObservedAt) <= s.settings.MaxAge && health.ObservedAt.After(previousObserved) {
			previousObserved = health.ObservedAt
			s.result.Checkpoint.LastObservedAt = cloneEventIntentTime(&health.ObservedAt)
		}
		s.result.Observations = min(EventAcknowledgmentIntentSequenceMax, s.result.Observations+1)
		s.result.EvidenceStatus, s.result.EvidenceReason = "unknown", reason
		if reason == "" {
			s.result.EvidenceStatus, s.result.EvidenceReason = "eligible", "complete_retained_intents"
		}
		s.result.Health = nil
		if readErr == nil {
			s.result.Health = &health
		}
		s.result.Alerts, s.result.Confirmations = observeEventIntentCheckpoint(&s.result.Checkpoint, s.proofs, health, reason, now)
		if err := s.publish(); err != nil {
			return err
		}
		if err := eventWorkerWait(s.ctx, s.settings.PollInterval); err != nil {
			return err
		}
		s.result.Alerts = []EventAcknowledgmentIntentNotification{}
		if err := s.acknowledge(); err != nil {
			return err
		}
	}
}
