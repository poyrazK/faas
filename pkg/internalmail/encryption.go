package internalmail

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const EncryptionPath = OperationsPath + "/encryption"

var (
	ErrEncryptionKeyUnavailable = errors.New("internal mail encryption key unavailable")
	ErrEncryptionRevision       = errors.New("internal mail encryption revision changed")
	ErrEncryptionKeyRequired    = errors.New("internal mail encryption key still required")
)

type EncryptionKeyStatus struct {
	KeyID              string     `json:"key_id"`
	Configured         bool       `json:"configured"`
	Active             bool       `json:"active"`
	RetiredAt          *time.Time `json:"retired_at,omitempty"`
	Payloads           int64      `json:"payloads"`
	DomainKeys         int64      `json:"domain_keys"`
	DKIMKeys           int64      `json:"dkim_keys"`
	Fingerprints       int64      `json:"fingerprints"`
	PrunedFingerprints int64      `json:"pruned_fingerprints"`
}

func (k EncryptionKeyStatus) required() bool {
	return k.Active || k.Payloads+k.DomainKeys+k.DKIMKeys+k.Fingerprints > 0
}

type EncryptionStatus struct {
	Revision    int64                 `json:"revision"`
	ActiveKeyID string                `json:"active_key_id"`
	LegacyKeyID string                `json:"legacy_key_id"`
	Versioned   bool                  `json:"versioned"`
	Keys        []EncryptionKeyStatus `json:"keys"`
}

type EncryptionRequest struct {
	OperatorAuditInput
	KeyID            string `json:"key_id"`
	ExpectedRevision *int64 `json:"expected_revision"`
	BatchSize        int    `json:"batch_size,omitempty"`
}

func (r EncryptionRequest) Validate(action string) error {
	if r.OperatorAuditInput.Validate() != nil || !validEncryptionKeyID(r.KeyID) || r.ExpectedRevision == nil || *r.ExpectedRevision < 0 {
		return ErrInvalid
	}
	switch action {
	case "activate", "retire":
		if r.BatchSize == 0 {
			return nil
		}
	case "rewrap":
		if validEncryptionBatch(r.BatchSize) {
			return nil
		}
	}
	return ErrInvalid
}

func validEncryptionBatch(size int) bool {
	return size >= 1 && size <= api.InternalMailMaxEncryptionBatchSize
}

type EncryptionBatch struct {
	DryRun     bool   `json:"dry_run"`
	KeyID      string `json:"key_id"`
	Revision   int64  `json:"revision"`
	BatchSize  int    `json:"batch_size"`
	DomainKeys int    `json:"domain_keys"`
	DKIMKeys   int    `json:"dkim_keys"`
	Payloads   int    `json:"payloads"`
	Rewrapped  int    `json:"rewrapped"`
	BatchFull  bool   `json:"batch_full"`
}

type encryptionStore interface {
	encryptionStatus(context.Context, *KeyringCodec) (EncryptionStatus, error)
	previewEncryption(context.Context, int, *KeyringCodec) (EncryptionBatch, error)
	changeEncryption(context.Context, string, EncryptionRequest, *KeyringCodec, func() bool) (OperationRecord, error)
	ConfigureEncryption(context.Context, *KeyringCodec) error
	refreshEncryptionSelection(context.Context, *KeyringCodec) error
}

func (s *Service) encryptionRotationEnabled() bool {
	return s.enabled() && s.EncryptionRotationEnabled != nil && s.EncryptionRotationEnabled()
}

func (s *Service) encryptionOperations() (encryptionStore, *KeyringCodec, error) {
	if !s.encryptionRotationEnabled() || !s.operationsEnabled() {
		return nil, nil, ErrDisabled
	}
	store, ok := s.Store.(encryptionStore)
	codec, supported := s.Codec.(*KeyringCodec)
	if !ok || !supported || codec == nil {
		return nil, nil, ErrInvalid
	}
	return store, codec, nil
}

func (s *Service) EncryptionStatus(ctx context.Context) (EncryptionStatus, error) {
	store, codec, err := s.encryptionOperations()
	if err != nil {
		return EncryptionStatus{}, err
	}
	return store.encryptionStatus(ctx, codec)
}

func (s *Service) PreviewEncryption(ctx context.Context, batchSize int) (EncryptionBatch, error) {
	store, codec, err := s.encryptionOperations()
	if err != nil {
		return EncryptionBatch{}, err
	}
	if !validEncryptionBatch(batchSize) {
		return EncryptionBatch{}, ErrInvalid
	}
	return store.previewEncryption(ctx, batchSize, codec)
}

func (s *Service) ChangeEncryption(ctx context.Context, action string, input EncryptionRequest) (OperationRecord, error) {
	store, codec, err := s.encryptionOperations()
	if err != nil {
		return OperationRecord{}, err
	}
	if input.Validate(action) != nil {
		return OperationRecord{}, ErrInvalid
	}
	record, err := store.changeEncryption(ctx, action, input, codec, func() bool { return s.encryptionRotationEnabled() && s.operationsEnabled() })
	if err != nil {
		return OperationRecord{}, err
	}
	// Reload current state, not the saved result: replaying an old activation
	// must never switch this process back to an older selection.
	if err := store.ConfigureEncryption(ctx, codec); err != nil {
		return OperationRecord{}, err
	}
	return record, nil
}
