package internalmail

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const OperationsPath = APIPath + "/operations"

type OperatorList struct {
	Limit  int
	After  string
	Status string
}

type OperatorPage[T any] struct {
	Items []T    `json:"items"`
	Next  string `json:"next,omitempty"`
}

type OperatorMessage struct {
	MessageTiming
	ID              string              `json:"id"`
	DomainID        string              `json:"domain_id"`
	ProducerID      string              `json:"producer_id,omitempty"`
	SenderAddress   string              `json:"sender_address,omitempty"`
	CreatedAt       time.Time           `json:"created_at"`
	PayloadPrunedAt *time.Time          `json:"payload_pruned_at,omitempty"`
	RecipientCount  int64               `json:"recipient_count"`
	Recipients      []OperatorRecipient `json:"recipients,omitempty"`
}

type OperatorRecipient struct {
	Recipient
	NextAttemptAt time.Time  `json:"next_attempt_at"`
	LeaseUntil    *time.Time `json:"lease_until,omitempty"`
}

type Suppression struct {
	Address   string    `json:"address"`
	Reason    string    `json:"reason"`
	CreatedAt time.Time `json:"created_at"`
}

// Actor is an operator's assertion under the shared operator credential, not
// an identity authenticated through customer login. Evidence is a reference,
// never the contents of a message, credential, or relay transcript.
type OperatorAuditInput struct {
	RequestID string `json:"request_id"`
	Actor     string `json:"actor"`
	Reason    string `json:"reason"`
	Evidence  string `json:"evidence"`
}

func (a OperatorAuditInput) Validate() error {
	if !canonicalOperatorID(a.RequestID) ||
		!regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.@-]{0,127}$`).MatchString(a.Actor) ||
		!regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`).MatchString(a.Reason) ||
		!regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9:/._-]{0,255}$`).MatchString(a.Evidence) {
		return ErrInvalid
	}
	return nil
}

type ReconcileRequest struct {
	OperatorAuditInput
	Outcome                string `json:"outcome"` // retry or submitted
	ConfirmedNotAccepted   bool   `json:"confirmed_not_accepted"`
	ConfirmedRelayAccepted bool   `json:"confirmed_relay_accepted"`
}

func (r ReconcileRequest) Validate() error {
	if err := r.OperatorAuditInput.Validate(); err != nil {
		return err
	}
	if r.Outcome == "retry" && r.ConfirmedNotAccepted && !r.ConfirmedRelayAccepted ||
		r.Outcome == "submitted" && r.ConfirmedRelayAccepted && !r.ConfirmedNotAccepted {
		return nil
	}
	return ErrInvalid
}

type OperationRecord struct {
	ID        string          `json:"id"`
	Action    string          `json:"action"`
	Resource  string          `json:"resource"`
	Actor     string          `json:"actor"`
	Reason    string          `json:"reason"`
	Evidence  string          `json:"evidence"`
	Result    json.RawMessage `json:"result"`
	CreatedAt time.Time       `json:"created_at"`
	replayed  bool
}

type operationsStore interface {
	operatorMessages(context.Context, OperatorList) (OperatorPage[OperatorMessage], error)
	operatorMessage(context.Context, string) (OperatorMessage, error)
	operatorEvents(context.Context, string, OperatorList) (OperatorPage[Event], error)
	operatorDomains(context.Context, OperatorList) (OperatorPage[Domain], error)
	operatorDomain(context.Context, string) (Domain, error)
	operatorSuppressions(context.Context, OperatorList) (OperatorPage[Suppression], error)
	operatorAudit(context.Context, OperatorList) (OperatorPage[OperationRecord], error)
	operatorReconcile(context.Context, string, ReconcileRequest, func() bool) (OperationRecord, error)
	operatorRetention(context.Context, OperatorAuditInput, RetentionPolicy, func(Message) ([]byte, error), func() bool) (OperationRecord, error)
}

func canonicalOperatorID(id string) bool {
	u, err := uuid.Parse(id)
	return err == nil && u != uuid.Nil && u.String() == id
}

func validateOperationsToken(token, service, reports string, sources map[string]FeedbackSource) error {
	if !validToken(token) || token == service || token == reports {
		return errors.New("internal mail: operations require a distinct token of at least 32 bytes")
	}
	for _, source := range sources {
		if token == source.Token {
			return errors.New("internal mail: operations and feedback credentials must be distinct")
		}
	}
	return nil
}

func (s *Service) operationsEnabled() bool {
	return s.enabled() && s.OperationsEnabled != nil && s.OperationsEnabled()
}

func (s *Service) operationStore() (operationsStore, error) {
	if !s.operationsEnabled() {
		return nil, ErrDisabled
	}
	store, ok := s.Store.(operationsStore)
	if !ok {
		return nil, ErrInvalid
	}
	return store, nil
}

func (s *Service) ReconcileUncertain(ctx context.Context, id string, input ReconcileRequest) (OperationRecord, error) {
	store, err := s.operationStore()
	if err != nil {
		return OperationRecord{}, err
	}
	if !canonicalOperatorID(id) || input.Validate() != nil {
		return OperationRecord{}, ErrInvalid
	}
	return store.operatorReconcile(ctx, id, input, s.operationsEnabled)
}

func (s *Service) OperatorRetainPayloads(ctx context.Context, input OperatorAuditInput) (OperationRecord, error) {
	store, err := s.operationStore()
	if err != nil {
		return OperationRecord{}, err
	}
	if !s.retentionEnabled() {
		return OperationRecord{}, ErrDisabled
	}
	if input.Validate() != nil || s.RetentionPolicy.validate() != nil || s.Codec == nil {
		return OperationRecord{}, ErrInvalid
	}
	if s.RetentionPolicy.DryRun {
		return OperationRecord{}, ErrRetentionDryRun
	}
	record, err := store.operatorRetention(ctx, input, s.RetentionPolicy, s.retentionFingerprint, func() bool { return s.operationsEnabled() && s.retentionEnabled() })
	if s.OnRetention != nil && !record.replayed {
		var result RetentionResult
		if err == nil {
			_ = json.Unmarshal(record.Result, &result)
		}
		s.OnRetention(result, err)
	}
	return record, err
}

func operatorPage[T any](items []T, limit int, key func(T) string) OperatorPage[T] {
	page := OperatorPage[T]{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		page.Next = key(items[limit-1])
	}
	return page
}

func validOperatorStatus(status string) bool {
	switch status {
	case "", "queued", "claimed", "submitting", "submitted", "accepted", "bounced", "complained", "suppressed", "failed", "uncertain", "deferred", "cancelled", "expired":
		return true
	}
	return false
}

func (o OperatorList) validate(uuidCursor bool) error {
	if o.Limit < 1 || o.Limit > api.InternalMailMaxOperatorPageSize || len(o.After) > api.InternalMailMaxOperatorCursorBytes || !validOperatorStatus(o.Status) {
		return ErrInvalid
	}
	if uuidCursor && o.After != "" && !canonicalOperatorID(o.After) {
		return ErrInvalid
	}
	return nil
}

func operatorAfterID(after string) string {
	if after == "" {
		return uuid.Nil.String()
	}
	return after
}
