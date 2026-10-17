package internalmail

import (
	"context"
	"errors"
	"time"
)

const DeliveryControlsPath = OperationsPath + "/delivery"

var (
	ErrDeliveryPaused  = errors.New("internal mail delivery is held")
	ErrControlRevision = errors.New("internal mail delivery control revision changed")
)

type DeliveryScope struct {
	Kind     string `json:"scope"`
	DomainID string `json:"domain_id,omitempty"`
}

func (s DeliveryScope) Validate() error {
	if s.Kind == "namespace" && s.DomainID == "" || s.Kind == "domain" && canonicalOperatorID(s.DomainID) {
		return nil
	}
	return ErrInvalid
}
func (s DeliveryScope) resource() string {
	if s.Kind == "namespace" {
		return "namespace"
	}
	return s.DomainID
}
func (s DeliveryScope) id() string { return operatorAfterID(s.DomainID) }

type DeliveryControlRequest struct {
	OperatorAuditInput
	DeliveryScope
	ExpectedRevision *int64 `json:"expected_revision"`
}

func (r DeliveryControlRequest) Validate() error {
	if r.OperatorAuditInput.Validate() != nil || r.DeliveryScope.Validate() != nil || r.ExpectedRevision == nil || *r.ExpectedRevision < 0 {
		return ErrInvalid
	}
	return nil
}

type DeliveryControlStatus struct {
	DeliveryScope
	Mode              string     `json:"mode"`
	Revision          int64      `json:"revision"`
	RequestID         string     `json:"request_id,omitempty"`
	UpdatedAt         *time.Time `json:"updated_at,omitempty"`
	NamespaceMode     string     `json:"namespace_mode"`
	Held              bool       `json:"held"`
	Drained           bool       `json:"drained"`
	Queued            int64      `json:"queued"`
	ActiveClaims      int64      `json:"active_claims"`
	ExpiredClaims     int64      `json:"expired_claims"`
	ActiveSubmissions int64      `json:"active_submissions"`
	Uncertain         int64      `json:"uncertain"`
	RelayOwned        int64      `json:"relay_owned"`
	Cancelled         int64      `json:"cancelled"`
	Expired           int64      `json:"expired"`
}

type CancellationRecipient struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}
type CancellationResult struct {
	ResourceID       string                  `json:"resource_id"`
	Cancelled        []string                `json:"cancelled"`
	AlreadyCancelled []string                `json:"already_cancelled"`
	Protected        []CancellationRecipient `json:"protected"`
	Complete         bool                    `json:"complete"`
}

type deliveryControlsStore interface {
	deliveryControlStatus(context.Context, DeliveryScope) (DeliveryControlStatus, error)
	changeDeliveryControl(context.Context, string, DeliveryControlRequest, func() bool) (OperationRecord, error)
	cancelMail(context.Context, string, bool, OperatorAuditInput, func() bool) (OperationRecord, error)
	dispatchPermittedFor(context.Context, string) (bool, error)
	recipientStopped(context.Context, string) (bool, error)
}

func (s *Service) deliveryControlsEnabled() bool {
	return s.enabled() && s.DeliveryControlsEnabled != nil && s.DeliveryControlsEnabled()
}
func (s *Service) controlsStore() (deliveryControlsStore, error) {
	if !s.deliveryControlsEnabled() || !s.operationsEnabled() {
		return nil, ErrDisabled
	}
	store, ok := s.Store.(deliveryControlsStore)
	if !ok {
		return nil, ErrInvalid
	}
	return store, nil
}
func (s *Service) DeliveryStatus(ctx context.Context, scope DeliveryScope) (DeliveryControlStatus, error) {
	store, err := s.controlsStore()
	if err != nil {
		return DeliveryControlStatus{}, err
	}
	if scope.Validate() != nil {
		return DeliveryControlStatus{}, ErrInvalid
	}
	return store.deliveryControlStatus(ctx, scope)
}
func (s *Service) ChangeDeliveryControl(ctx context.Context, mode string, input DeliveryControlRequest) (OperationRecord, error) {
	store, err := s.controlsStore()
	if err != nil {
		return OperationRecord{}, err
	}
	if input.Validate() != nil || mode != "running" && mode != "paused" && mode != "draining" {
		return OperationRecord{}, ErrInvalid
	}
	return store.changeDeliveryControl(ctx, mode, input, func() bool { return s.deliveryControlsEnabled() && s.operationsEnabled() })
}
func (s *Service) CancelMail(ctx context.Context, id string, message bool, input OperatorAuditInput) (OperationRecord, error) {
	store, err := s.controlsStore()
	if err != nil {
		return OperationRecord{}, err
	}
	if !canonicalOperatorID(id) || input.Validate() != nil {
		return OperationRecord{}, ErrInvalid
	}
	return store.cancelMail(ctx, id, message, input, func() bool { return s.deliveryControlsEnabled() && s.operationsEnabled() })
}

// Persisted holds and cancellations remain effective when operator access is
// disabled. The feature flag gates new operations, not previously saved intent.
func (s *Service) deliveryWorkPermitted(ctx context.Context, r Recipient) (bool, error) {
	if store, ok := s.Store.(deliveryControlsStore); ok {
		return store.dispatchPermittedFor(ctx, r.ID)
	}
	if s.deliveryControlsEnabled() {
		return false, ErrInvalid
	}
	return true, nil
}
func (s *Service) settleMail(ctx context.Context, r Recipient, status, code string, next time.Time) error {
	err := s.Store.Settle(ctx, r, status, code, next)
	if errors.Is(err, ErrLeaseLost) {
		if store, ok := s.Store.(deliveryControlsStore); ok {
			cancelled, readErr := store.recipientStopped(ctx, r.ID)
			if readErr == nil && cancelled {
				return nil
			}
		}
	}
	return err
}
