package internalmail

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const SuppressionControlsPath = OperationsPath + "/suppressions"

var (
	ErrSuppressionRevision  = errors.New("internal mail suppression revision changed")
	ErrSuppressionProtected = errors.New("internal mail complaint suppression is protected")
	ErrRecipientSuppressed  = errors.New("internal mail recipient is suppressed")
)

// Addresses share the existing namespace-wide, case-insensitive policy.
// Display names, patterns, whitespace and noncanonical quoted forms are denied.
func NormalizeSuppressionAddress(value string) (string, error) {
	address, err := parseAddress(value)
	if err != nil || len(value) > api.InternalMailMaxSuppressionAddressBytes || address.Name != "" || !strings.EqualFold(address.Address, value) || strings.ContainsAny(value, "*?") {
		return "", ErrInvalid
	}
	return strings.ToLower(address.Address), nil
}

type suppressionSource struct{ kind, id string }

type SuppressionEvidence struct {
	SourceKind  string `json:"source_kind"`
	SourceID    string `json:"source_id,omitempty"`
	RecipientID string `json:"recipient_id,omitempty"`
	EventRef    string `json:"event_ref,omitempty"`
	RequestID   string `json:"request_id,omitempty"`
}

type SuppressionStatus struct {
	Address           string               `json:"address"`
	Blocked           bool                 `json:"blocked"`
	Revision          int64                `json:"revision"`
	LastBlockRevision int64                `json:"last_block_revision"`
	Reason            string               `json:"reason,omitempty"`
	Cause             string               `json:"cause,omitempty"`
	Evidence          *SuppressionEvidence `json:"evidence,omitempty"`
	UpdatedAt         *time.Time           `json:"updated_at,omitempty"`
}

type SuppressionEvent struct {
	Revision  int64               `json:"revision"`
	Blocked   bool                `json:"blocked"`
	Reason    string              `json:"reason"`
	Cause     string              `json:"cause"`
	Evidence  SuppressionEvidence `json:"evidence"`
	CreatedAt time.Time           `json:"created_at"`
}

type SuppressionRequest struct {
	OperatorAuditInput
	Address           string `json:"address"`
	ExpectedRevision  *int64 `json:"expected_revision"`
	ConfirmedReviewed bool   `json:"confirmed_reviewed,omitempty"`
}

func (r SuppressionRequest) Validate(action string) error {
	address, err := NormalizeSuppressionAddress(r.Address)
	if err != nil || address != r.Address || r.OperatorAuditInput.Validate() != nil || r.ExpectedRevision == nil || *r.ExpectedRevision < 0 {
		return ErrInvalid
	}
	if action == "add" && !r.ConfirmedReviewed || action == "release" && r.ConfirmedReviewed {
		return nil
	}
	return ErrInvalid
}

func suppressionHistoryList(list OperatorList) (int64, error) {
	if list.validate(false) != nil || list.Status != "" {
		return 0, ErrInvalid
	}
	if list.After == "" {
		return 0, nil
	}
	after, err := strconv.ParseInt(list.After, 10, 64)
	if err != nil || after < 1 || strconv.FormatInt(after, 10) != list.After {
		return 0, ErrInvalid
	}
	return after, nil
}

type suppressionControlsStore interface {
	suppressionStatus(context.Context, string) (SuppressionStatus, error)
	suppressionHistory(context.Context, string, OperatorList) (OperatorPage[SuppressionEvent], error)
	changeSuppression(context.Context, string, SuppressionRequest, func() bool) (OperationRecord, error)
}

type suppressionSafetyStore interface {
	recipientSuppressed(context.Context, string) (bool, error)
}

func (s *Service) suppressionControlsEnabled() bool {
	return s.enabled() && s.SuppressionControlsEnabled != nil && s.SuppressionControlsEnabled()
}

func (s *Service) suppressionOperations() (suppressionControlsStore, error) {
	if !s.suppressionControlsEnabled() || !s.operationsEnabled() {
		return nil, ErrDisabled
	}
	store, ok := s.Store.(suppressionControlsStore)
	if !ok {
		return nil, ErrInvalid
	}
	return store, nil
}

func (s *Service) SuppressionStatus(ctx context.Context, address string) (SuppressionStatus, error) {
	store, err := s.suppressionOperations()
	if err != nil {
		return SuppressionStatus{}, err
	}
	address, err = NormalizeSuppressionAddress(address)
	if err != nil {
		return SuppressionStatus{}, err
	}
	return store.suppressionStatus(ctx, address)
}

func (s *Service) SuppressionHistory(ctx context.Context, address string, list OperatorList) (OperatorPage[SuppressionEvent], error) {
	store, err := s.suppressionOperations()
	if err != nil {
		return OperatorPage[SuppressionEvent]{}, err
	}
	address, err = NormalizeSuppressionAddress(address)
	if err != nil {
		return OperatorPage[SuppressionEvent]{}, err
	}
	if _, err := suppressionHistoryList(list); err != nil {
		return OperatorPage[SuppressionEvent]{}, err
	}
	return store.suppressionHistory(ctx, address, list)
}

func (s *Service) ChangeSuppression(ctx context.Context, action string, input SuppressionRequest) (OperationRecord, error) {
	store, err := s.suppressionOperations()
	if err != nil {
		return OperationRecord{}, err
	}
	input.Address, err = NormalizeSuppressionAddress(input.Address)
	if err != nil || input.Validate(action) != nil {
		return OperationRecord{}, ErrInvalid
	}
	return store.changeSuppression(ctx, action, input, func() bool { return s.suppressionControlsEnabled() && s.operationsEnabled() })
}

func (s *Service) recipientSuppressed(ctx context.Context, r Recipient) (bool, error) {
	if store, ok := s.Store.(suppressionSafetyStore); ok {
		return store.recipientSuppressed(ctx, r.ID)
	}
	if s.suppressionControlsEnabled() {
		return false, ErrInvalid
	}
	return s.Store.Suppressed(ctx, r.Address)
}

func suppressionControlsRoute(path string) bool {
	return strings.HasPrefix(path, SuppressionControlsPath+"/")
}
