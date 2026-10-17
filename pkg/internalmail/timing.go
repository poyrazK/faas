package internalmail

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var (
	ErrMessageExpired = errors.New("internal mail message expired")
	ErrMessageNotDue  = errors.New("internal mail message is not due")
)

// MessageTiming uses absolute times so identical retries never extend a
// schedule or deadline. Missing fields preserve legacy immediate delivery.
type MessageTiming struct {
	SendAt    *time.Time `json:"send_at,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

func (t MessageTiming) IsZero() bool { return t.SendAt == nil && t.ExpiresAt == nil }

func (t MessageTiming) normalized() (MessageTiming, error) {
	for _, field := range []**time.Time{&t.SendAt, &t.ExpiresAt} {
		if *field == nil {
			continue
		}
		if (*field).IsZero() || (*field).Year() < 1 || (*field).Year() > 9999 {
			return t, ErrInvalid
		}
		// PostgreSQL stores microseconds. Copy rather than mutate the caller's
		// pointer so replay and concurrent callers share a canonical request.
		at := (*field).UTC().Truncate(time.Microsecond)
		*field = &at
	}
	if t.SendAt != nil && t.ExpiresAt != nil && (!t.ExpiresAt.After(*t.SendAt) || t.ExpiresAt.Sub(*t.SendAt) > api.InternalMailMaxQueueAge) {
		return t, ErrInvalid
	}
	return t, nil
}

func sameTiming(a, b MessageTiming) bool {
	return sameOptionalTime(a.SendAt, b.SendAt) && sameOptionalTime(a.ExpiresAt, b.ExpiresAt)
}
func sameOptionalTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

type timingStore interface {
	enqueueTimed(context.Context, Message, []Recipient, domainAuthorization, *Producer, func() bool) (Message, bool, error)
	claimTimed(context.Context, domainAuthorization, []string, bool) (Recipient, error)
	beginTimedSubmission(context.Context, Recipient, string, domainAuthorization, []string, bool) error
	expireTimed(context.Context) error
}

func (s *Service) schedulingEnabled() bool {
	return s.enabled() && s.SchedulingEnabled != nil && s.SchedulingEnabled()
}

func (m Message) submissionDeadline() *time.Time {
	if m.IsZero() {
		return nil
	}
	base := m.CreatedAt
	if m.SendAt != nil {
		base = *m.SendAt
	}
	deadline := base.Add(api.InternalMailMaxQueueAge)
	if m.ExpiresAt != nil && m.ExpiresAt.Before(deadline) {
		deadline = *m.ExpiresAt
	}
	return &deadline
}

func (s *Service) checkMessageTiming(m Message) error {
	if m.IsZero() {
		return nil
	}
	if !s.now().Before(*m.submissionDeadline()) {
		return ErrMessageExpired
	}
	if !s.schedulingEnabled() {
		return ErrDisabled
	}
	if m.SendAt != nil && s.now().Before(*m.SendAt) {
		return ErrMessageNotDue
	}
	return nil
}

func timingSettlement(err error) (string, string) {
	if errors.Is(err, ErrMessageExpired) {
		return "expired", "message_expired"
	}
	if errors.Is(err, ErrMessageNotDue) {
		return "queued", "message_not_due"
	}
	return "queued", "scheduling_disabled"
}

func (s *Service) enqueueWithTiming(ctx context.Context, m Message, recipients []Recipient) (Message, bool, error) {
	if !s.schedulingEnabled() {
		return Message{}, false, ErrDisabled
	}
	var producer *Producer
	if m.ProducerID != "" {
		p, err := s.producer(m.ProducerID)
		if err != nil {
			return Message{}, false, err
		}
		if !p.allows(m.SenderAddress) {
			return Message{}, false, ErrProducerSender
		}
		producer = &p
	}
	store, ok := s.Store.(timingStore)
	if !ok {
		return Message{}, false, ErrInvalid
	}
	return store.enqueueTimed(ctx, m, recipients, s.authorizationRequirement(), producer, func() bool {
		return s.schedulingEnabled() && (producer == nil || s.producersEnabled())
	})
}
