package internalmail

import (
	"context"
	"errors"
	"strings"
	"time"
)

const RecoveryPath = OperationsPath + "/recovery"

var (
	ErrRecoveryHeld     = errors.New("internal mail database recovery is held")
	ErrRecoveryRevision = errors.New("internal mail recovery revision changed")
)

type RecoveryStatus struct {
	RecoveryID        string     `json:"recovery_id,omitempty"`
	Revision          int64      `json:"revision"`
	Held              bool       `json:"held"`
	UpdatedAt         *time.Time `json:"updated_at,omitempty"`
	Quarantined       int64      `json:"quarantined"`
	ActiveSubmissions int64      `json:"active_submissions"`
}

// The preview contains identifiers and custody metadata, never payloads,
// addresses, encryption material or raw relay observations.
type RecoveryRecipient struct {
	ID         string     `json:"id"`
	MessageID  string     `json:"message_id"`
	Status     string     `json:"status"`
	LeaseUntil *time.Time `json:"lease_until,omitempty"`
}

type RecoveryPreview struct {
	RecoveryStatus
	OperatorPage[RecoveryRecipient]
}

type RecoveryReviewRequest struct {
	ReconcileRequest
	RecoveryID       string `json:"recovery_id"`
	ExpectedRevision *int64 `json:"expected_revision"`
	ExpectedStatus   string `json:"expected_status"`
}

func (r RecoveryReviewRequest) Validate() error {
	if r.ReconcileRequest.Validate() != nil || !canonicalOperatorID(r.RecoveryID) || r.ExpectedRevision == nil || *r.ExpectedRevision < 1 || !recoveryPending(r.ExpectedStatus) {
		return ErrInvalid
	}
	return nil
}

func recoveryPending(status string) bool {
	switch status {
	case "queued", "deferred", "claimed", "submitting", "uncertain", "submitted":
		return true
	}
	return false
}

type RecoveryReleaseRequest struct {
	OperatorAuditInput
	RecoveryID          string `json:"recovery_id"`
	ExpectedRevision    *int64 `json:"expected_revision"`
	ConfirmedPolicy     bool   `json:"confirmed_policy"`
	ConfirmedAccess     bool   `json:"confirmed_access"`
	ConfirmedConsumers  bool   `json:"confirmed_consumers"`
	ConfirmedRelay      bool   `json:"confirmed_relay"`
	ConfirmedQuarantine bool   `json:"confirmed_quarantine"`
}

func (r RecoveryReleaseRequest) Validate() error {
	if r.OperatorAuditInput.Validate() != nil || !canonicalOperatorID(r.RecoveryID) || r.ExpectedRevision == nil || *r.ExpectedRevision < 1 || !r.ConfirmedPolicy || !r.ConfirmedAccess || !r.ConfirmedConsumers || !r.ConfirmedRelay || !r.ConfirmedQuarantine {
		return ErrInvalid
	}
	return nil
}

type recoverySafetyStore interface {
	checkRecovery(context.Context) error
}

type recoveryStore interface {
	recoveryStatus(context.Context) (RecoveryStatus, error)
	recoveryPreview(context.Context, OperatorList) (RecoveryPreview, error)
	reviewRecovery(context.Context, string, RecoveryReviewRequest, func() bool) (OperationRecord, error)
	releaseRecovery(context.Context, RecoveryReleaseRequest, *KeyringCodec, func() bool) (OperationRecord, error)
}

func (s *Service) recoveryEnabled() bool {
	return s.enabled() && s.RecoveryEnabled != nil && s.RecoveryEnabled()
}

func (s *Service) recoveryOperations() (recoveryStore, error) {
	if !s.recoveryEnabled() || !s.operationsEnabled() {
		return nil, ErrDisabled
	}
	store, ok := s.Store.(recoveryStore)
	if !ok {
		return nil, ErrInvalid
	}
	return store, nil
}

// Persisted recovery holds survive management rollback. Unsupported stores
// must fail closed when recovery is explicitly enabled.
func (s *Service) checkRecovery(ctx context.Context) error {
	if store, ok := s.Store.(recoverySafetyStore); ok {
		return store.checkRecovery(ctx)
	}
	if s.recoveryEnabled() {
		return ErrInvalid
	}
	return nil
}

func (s *Service) RecoveryStatus(ctx context.Context) (RecoveryStatus, error) {
	store, err := s.recoveryOperations()
	if err != nil {
		return RecoveryStatus{}, err
	}
	return store.recoveryStatus(ctx)
}

func (s *Service) PreviewRecovery(ctx context.Context, list OperatorList) (RecoveryPreview, error) {
	store, err := s.recoveryOperations()
	if err != nil {
		return RecoveryPreview{}, err
	}
	if list.validate(true) != nil || list.Status != "" {
		return RecoveryPreview{}, ErrInvalid
	}
	return store.recoveryPreview(ctx, list)
}

func (s *Service) ReviewRecovery(ctx context.Context, id string, input RecoveryReviewRequest) (OperationRecord, error) {
	store, err := s.recoveryOperations()
	if err != nil {
		return OperationRecord{}, err
	}
	if !canonicalOperatorID(id) || input.Validate() != nil {
		return OperationRecord{}, ErrInvalid
	}
	return store.reviewRecovery(ctx, id, input, func() bool { return s.recoveryEnabled() && s.operationsEnabled() })
}

func (s *Service) ReleaseRecovery(ctx context.Context, input RecoveryReleaseRequest) (OperationRecord, error) {
	store, err := s.recoveryOperations()
	if err != nil {
		return OperationRecord{}, err
	}
	codec, ok := s.Codec.(*KeyringCodec)
	if input.Validate() != nil || !ok {
		return OperationRecord{}, ErrInvalid
	}
	return store.releaseRecovery(ctx, input, codec, func() bool { return s.recoveryEnabled() && s.operationsEnabled() })
}

func recoveryRoute(path string) bool {
	return path == RecoveryPath || strings.HasPrefix(path, RecoveryPath+"/")
}
