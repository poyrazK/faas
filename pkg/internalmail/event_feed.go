package internalmail

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const EventFeedOperationsPath = OperationsPath + "/event-consumers"

var ErrEventFeedGeneration = errors.New("internal mail event consumer generation changed")

// DeliveryEvent is an immutable observation, not a message-state snapshot.
// It deliberately excludes addresses, diagnostics and the source event identity.
type DeliveryEvent struct {
	ID          string    `json:"id"`
	Sequence    int64     `json:"sequence,string"`
	MessageID   string    `json:"message_id"`
	RecipientID string    `json:"recipient_id"`
	DomainID    string    `json:"domain_id"`
	Kind        string    `json:"kind"`
	OccurredAt  time.Time `json:"occurred_at"`
	PublishedAt time.Time `json:"published_at"`
}
type EventFeedBatch struct {
	ProducerID string          `json:"producer_id"`
	Consumer   string          `json:"consumer"`
	Generation int64           `json:"generation"`
	Cursor     string          `json:"cursor"`
	NextCursor string          `json:"next_cursor"`
	Receipt    string          `json:"receipt,omitempty"`
	Events     []DeliveryEvent `json:"events"`
}
type EventFeedAckInput struct {
	Receipt    string `json:"receipt"`
	Generation int64  `json:"generation"`
}

func (a EventFeedAckInput) Validate() error {
	if !canonicalOperatorID(a.Receipt) || a.Generation < 1 {
		return ErrInvalid
	}
	return nil
}

type EventFeedAck struct {
	ProducerID string `json:"producer_id"`
	Consumer   string `json:"consumer"`
	Generation int64  `json:"generation"`
	Receipt    string `json:"receipt"`
	Cursor     string `json:"cursor"`
}
type EventConsumerStatus struct {
	ProducerID     string     `json:"producer_id"`
	Consumer       string     `json:"consumer"`
	Generation     int64      `json:"generation"`
	Cursor         string     `json:"cursor"`
	PendingReceipt string     `json:"pending_receipt,omitempty"`
	Backlog        int64      `json:"backlog"`
	OldestSeconds  float64    `json:"oldest_seconds"`
	CreatedAt      time.Time  `json:"created_at"`
	LastPullAt     time.Time  `json:"last_pull_at"`
	LastAckAt      *time.Time `json:"last_ack_at,omitempty"`
}
type EventFeedReplayRequest struct {
	OperatorAuditInput
	ProducerID         string `json:"producer_id"`
	Consumer           string `json:"consumer"`
	Cursor             string `json:"cursor"`
	ExpectedGeneration *int64 `json:"expected_generation"`
}

func validEventConsumer(name string) bool {
	return len(name) <= api.InternalMailEventConsumerNameBytes && validNamespace(name)
}

func EventConsumerRoute(producer, consumer string) (string, error) {
	if !validProducerID(producer) || !validEventConsumer(consumer) {
		return "", ErrInvalid
	}
	return ProducersPath + "/" + producer + "/event-consumers/" + consumer, nil
}
func validDeliveryEventKind(kind string) bool {
	switch kind {
	case "queued", "submitted", "accepted", "bounced", "complained", "suppressed", "failed", "uncertain", "deferred", "retry", "cancelled", "expired":
		return true
	}
	return false
}
func (r EventFeedReplayRequest) Validate() error {
	if r.OperatorAuditInput.Validate() != nil || !validProducerID(r.ProducerID) || !validEventConsumer(r.Consumer) ||
		r.ExpectedGeneration == nil || *r.ExpectedGeneration < 1 || r.Cursor != "" && !canonicalOperatorID(r.Cursor) {
		return ErrInvalid
	}
	return nil
}

type eventFeedStore interface {
	publishEventFeed(context.Context, func() bool) (int, error)
	pullEventFeed(context.Context, string, string, int, func() bool) (EventFeedBatch, error)
	ackEventFeed(context.Context, string, string, EventFeedAckInput, func() bool) (EventFeedAck, error)
	eventConsumerStatus(context.Context, string, string) (EventConsumerStatus, error)
	eventConsumers(context.Context, string, OperatorList) (OperatorPage[EventConsumerStatus], error)
	replayEventConsumer(context.Context, EventFeedReplayRequest, func() bool) (OperationRecord, error)
}

func (s *Service) eventFeedEnabled() bool {
	return s.enabled() && s.EventFeedEnabled != nil && s.EventFeedEnabled()
}
func (s *Service) eventFeedRunning() bool { return s.eventFeedEnabled() && s.producersEnabled() }
func (s *Service) feedProducer(id string) (eventFeedStore, error) {
	if !s.eventFeedEnabled() {
		return nil, ErrDisabled
	}
	if err := s.producerLifecycleSupported(); err != nil {
		return nil, err
	}
	if _, err := s.producer(id); err != nil {
		return nil, err
	}
	store, ok := s.Store.(eventFeedStore)
	if !ok {
		return nil, ErrInvalid
	}
	return store, nil
}
func (s *Service) eventProducerEnabled(id string) bool {
	_, err := s.producer(id)
	return s.eventFeedEnabled() && err == nil
}
func (s *Service) PublishEventFeed(ctx context.Context) (int, error) {
	if !s.eventFeedRunning() {
		return 0, ErrDisabled
	}
	store, ok := s.Store.(eventFeedStore)
	if !ok {
		return 0, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, api.InternalMailEventFeedPublishTimeout)
	defer cancel()
	return store.publishEventFeed(ctx, s.eventFeedRunning)
}
func (s *Service) PullEventFeed(ctx context.Context, producer, consumer string, limit int) (EventFeedBatch, error) {
	store, err := s.feedProducer(producer)
	if err != nil {
		return EventFeedBatch{}, err
	}
	if !validEventConsumer(consumer) || limit < 1 || limit > api.InternalMailEventFeedMaxPageSize {
		return EventFeedBatch{}, ErrInvalid
	}
	return store.pullEventFeed(ctx, producer, consumer, limit, func() bool { return s.eventProducerEnabled(producer) })
}
func (s *Service) AckEventFeed(ctx context.Context, producer, consumer string, input EventFeedAckInput) (EventFeedAck, error) {
	store, err := s.feedProducer(producer)
	if err != nil {
		return EventFeedAck{}, err
	}
	if !validEventConsumer(consumer) || input.Validate() != nil {
		return EventFeedAck{}, ErrInvalid
	}
	return store.ackEventFeed(ctx, producer, consumer, input, func() bool { return s.eventProducerEnabled(producer) })
}
func (s *Service) ProducerEventConsumer(ctx context.Context, producer, consumer string) (EventConsumerStatus, error) {
	store, err := s.feedProducer(producer)
	if err != nil {
		return EventConsumerStatus{}, err
	}
	if !validEventConsumer(consumer) {
		return EventConsumerStatus{}, ErrInvalid
	}
	return store.eventConsumerStatus(ctx, producer, consumer)
}
func (s *Service) eventFeedOperations() (eventFeedStore, error) {
	if !s.eventFeedEnabled() || !s.operationsEnabled() {
		return nil, ErrDisabled
	}
	store, ok := s.Store.(eventFeedStore)
	if !ok {
		return nil, ErrInvalid
	}
	return store, nil
}
func (s *Service) ReplayEventConsumer(ctx context.Context, input EventFeedReplayRequest) (OperationRecord, error) {
	store, err := s.eventFeedOperations()
	if err != nil {
		return OperationRecord{}, err
	}
	if input.Validate() != nil {
		return OperationRecord{}, ErrInvalid
	}
	return store.replayEventConsumer(ctx, input, func() bool { return s.eventFeedEnabled() && s.operationsEnabled() })
}
func (s *Service) RunEventFeed(ctx context.Context, onError func(error)) {
	ticker := time.NewTicker(api.InternalMailEventFeedPollInterval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		_, err := s.PublishEventFeed(ctx)
		if err != nil && !errors.Is(err, ErrDisabled) && ctx.Err() == nil && onError != nil {
			onError(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
