package internalmail

import (
	"context"
	"errors"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

const ProducersPath = APIPath + "/producers"

var ErrProducerSender = errors.New("internal mail producer sender is not allowed")

type ProducerUsage struct {
	ProducerID            string `json:"producer_id"`
	Daily                 int64  `json:"daily"`
	Pending               int64  `json:"pending"`
	DailyRecipientLimit   int    `json:"daily_recipient_limit"`
	PendingRecipientLimit int    `json:"pending_recipient_limit"`
}

type producerStore interface {
	enqueueProduced(context.Context, Message, []Recipient, domainAuthorization, Producer, func() bool) (Message, bool, error)
	claimWithProducers(context.Context, domainAuthorization, []string) (Recipient, error)
	beginProducerSubmission(context.Context, Recipient, string, domainAuthorization, []string) error
	producerMessage(context.Context, string, string) (OperatorMessage, error)
	producerEvents(context.Context, string, string, OperatorList) (OperatorPage[Event], error)
	producerUsage(context.Context, string) (ProducerUsage, error)
}

func (s *Service) producersEnabled() bool {
	return s.enabled() && s.ProducersEnabled != nil && s.ProducersEnabled()
}

func (s *Service) producer(id string) (Producer, error) {
	if !s.producersEnabled() {
		return Producer{}, ErrDisabled
	}
	p, ok := s.Producers[id]
	if !ok || !validProducerID(id) || p.ID != id {
		return Producer{}, ErrNotFound
	}
	if !producerSendersValid(p.From) || p.DailyRecipientLimit < 1 || p.DailyRecipientLimit > api.InternalMailDailyRecipients || p.PendingRecipientLimit < 1 || p.PendingRecipientLimit > api.InternalMailMaxPending {
		return Producer{}, ErrInvalid
	}
	return p, nil
}

// These pairs are trusted configuration, never a request-supplied filter.
// An absent producer or withdrawn sender cannot start a new submission.
// Disabling access does not erase durable ownership or existing age limits.
func (s *Service) allowedProducerSenders() []string {
	allowed := []string{}
	if !s.producersEnabled() {
		return allowed
	}
	for id := range s.Producers {
		p, err := s.producer(id)
		if err != nil {
			continue
		}
		for _, from := range p.From {
			allowed = append(allowed, id+":"+from)
		}
	}
	sort.Strings(allowed)
	return allowed
}

func (s *Service) producerMessagePermitted(m Message) bool {
	if deploymentFailedMessage(m) && !s.deploymentFailedOutboxEnabled() {
		return false
	}
	if agentRunDeliveredMessage(m) && !s.agentRunDeliveredOutboxEnabled() {
		return false
	}
	if subscriptionEndedMessage(m) && !s.subscriptionEndedOutboxEnabled() {
		return false
	}
	if quotaWarningMessage(m) && !s.quotaWarningOutboxEnabled() {
		return false
	}
	if nonpaymentDeletionMessage(m) && !s.nonpaymentDeletionOutboxEnabled() {
		return false
	}
	if accountSuspendedMessage(m) && !s.accountSuspendedOutboxEnabled() {
		return false
	}
	if accountRestoredMessage(m) && !s.accountRestoredOutboxEnabled() {
		return false
	}
	if paymentFailedMessage(m) && !s.paymentFailedOutboxEnabled() {
		return false
	}
	if m.ProducerID == "" {
		return true
	}
	p, err := s.producer(m.ProducerID)
	return err == nil && p.allows(m.SenderAddress)
}

func (s *Service) SubmitProduced(ctx context.Context, id, key string, request SendRequest) (MessageResponse, error) {
	p, err := s.producer(id)
	if err != nil {
		return MessageResponse{}, err
	}
	if err := s.producerLifecycleSupported(); err != nil {
		return MessageResponse{}, err
	}
	return s.submitMessage(ctx, key, request, &p)
}

func (s *Service) ProducerMessage(ctx context.Context, producerID, id string) (OperatorMessage, error) {
	if _, err := s.producer(producerID); err != nil {
		return OperatorMessage{}, err
	}
	if !canonicalOperatorID(id) {
		return OperatorMessage{}, ErrInvalid
	}
	store, ok := s.Store.(producerStore)
	if !ok {
		return OperatorMessage{}, ErrInvalid
	}
	return store.producerMessage(ctx, producerID, id)
}

func (s *Service) ProducerEvents(ctx context.Context, producerID, id string, o OperatorList) (OperatorPage[Event], error) {
	if _, err := s.producer(producerID); err != nil {
		return OperatorPage[Event]{}, err
	}
	if !canonicalOperatorID(id) || o.validate(false) != nil {
		return OperatorPage[Event]{}, ErrInvalid
	}
	store, ok := s.Store.(producerStore)
	if !ok {
		return OperatorPage[Event]{}, ErrInvalid
	}
	return store.producerEvents(ctx, producerID, id, o)
}

func (s *Service) ProducerUsage(ctx context.Context, id string) (ProducerUsage, error) {
	p, err := s.producer(id)
	if err != nil {
		return ProducerUsage{}, err
	}
	store, ok := s.Store.(producerStore)
	if !ok {
		return ProducerUsage{}, ErrInvalid
	}
	usage, err := store.producerUsage(ctx, id)
	usage.DailyRecipientLimit, usage.PendingRecipientLimit = p.DailyRecipientLimit, p.PendingRecipientLimit
	return usage, err
}

// ProducerMessagesRoute is also used by the Unix-only platform transport.
func ProducerMessagesRoute(id string) (string, error) {
	if !validProducerID(id) {
		return "", ErrInvalid
	}
	return ProducersPath + "/" + id + "/messages", nil
}
