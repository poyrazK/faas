package internalmail

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const ProducerLifecyclePath = OperationsPath + "/producer-access"
const producerSuspendedCode = "producer_suspended"

var (
	ErrProducerCredential = errors.New("internal mail producer credential is unavailable")
	ErrProducerSuspended  = errors.New("internal mail producer is suspended")
	ErrProducerRevision   = errors.New("internal mail producer access revision changed")
)

func producerCredentialID(producer, token string) string {
	if !validToken(token) {
		return ""
	}
	hash := sha256.Sum256([]byte("gregale-internal-mail-producer-v1\x00" + producer + "\x00" + token))
	return hex.EncodeToString(hash[:])
}

func validCredentialID(id string) bool {
	if len(id) != sha256.Size*2 {
		return false
	}
	for _, c := range id {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// Only these nonsecret identifiers may enter SQL, operator requests or audit.
type producerCredentialConfig struct {
	ProducerID       string
	PrimaryID        string
	ReplacementID    string
	AllowReplacement bool
}

func (s *Service) producerCredentialConfig(id string) producerCredentialConfig {
	p := s.Producers[id]
	return producerCredentialConfig{ProducerID: id, PrimaryID: producerCredentialID(id, p.Token), ReplacementID: producerCredentialID(id, p.ReplacementToken), AllowReplacement: s.producerLifecycleEnabled()}
}

type producerAuthentication struct {
	ProducerID   string
	CredentialID string
	PrimaryID    string
	Enabled      func() bool
}

type producerAuthenticationKey struct{}

type ProducerLifecycleRequest struct {
	OperatorAuditInput
	ProducerID        string `json:"producer_id"`
	ExpectedRevision  *int64 `json:"expected_revision"`
	CredentialID      string `json:"credential_id,omitempty"`
	OverlapSeconds    *int32 `json:"overlap_seconds,omitempty"`
	ConfirmedReviewed bool   `json:"confirmed_reviewed,omitempty"`
}

func (r ProducerLifecycleRequest) Validate(action string) error {
	if r.OperatorAuditInput.Validate() != nil || !validProducerID(r.ProducerID) || r.ExpectedRevision == nil || *r.ExpectedRevision < 0 {
		return ErrInvalid
	}
	switch action {
	case "rotate":
		if !validCredentialID(r.CredentialID) || r.OverlapSeconds == nil || *r.OverlapSeconds < 0 || *r.OverlapSeconds > int32(api.InternalMailMaxCredentialOverlap/time.Second) || r.ConfirmedReviewed {
			return ErrInvalid
		}
	case "revoke":
		if !validCredentialID(r.CredentialID) || r.OverlapSeconds != nil || r.ConfirmedReviewed {
			return ErrInvalid
		}
	case "suspend", "resume":
		if r.CredentialID != "" || r.OverlapSeconds != nil || r.ConfirmedReviewed != (action == "resume") {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

type ProducerCredentialStatus struct {
	ID         string     `json:"id"`
	Configured bool       `json:"configured"`
	Active     bool       `json:"active"`
	Usable     bool       `json:"usable"`
	RetiresAt  *time.Time `json:"retires_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

type ProducerAccessStatus struct {
	ProducerID              string                     `json:"producer_id"`
	Revision                int64                      `json:"revision"`
	Suspended               bool                       `json:"suspended"`
	ActiveCredentialID      string                     `json:"active_credential_id,omitempty"`
	PrimaryCredentialID     string                     `json:"primary_credential_id,omitempty"`
	ReplacementCredentialID string                     `json:"replacement_credential_id,omitempty"`
	Credentials             []ProducerCredentialStatus `json:"credentials"`
	UpdatedAt               *time.Time                 `json:"updated_at,omitempty"`
}

type producerLifecycleStore interface {
	checkProducerAuthentication(context.Context, producerAuthentication) error
	producerAccessStatus(context.Context, producerCredentialConfig) (ProducerAccessStatus, error)
	changeProducerAccess(context.Context, string, ProducerLifecycleRequest, producerCredentialConfig, func() bool) (OperationRecord, error)
}

func (s *Service) producerLifecycleEnabled() bool {
	return s.enabled() && s.ProducerLifecycleEnabled != nil && s.ProducerLifecycleEnabled()
}

func (s *Service) producerLifecycleSupported() error {
	if s.producerLifecycleEnabled() {
		if _, ok := s.Store.(producerLifecycleStore); !ok {
			return ErrInvalid
		}
	}
	return nil
}

func (s *Service) producerLifecycleOperations() (producerLifecycleStore, error) {
	if !s.producerLifecycleEnabled() || !s.operationsEnabled() {
		return nil, ErrDisabled
	}
	store, ok := s.Store.(producerLifecycleStore)
	if !ok {
		return nil, ErrInvalid
	}
	return store, nil
}

func (s *Service) ProducerAccessStatus(ctx context.Context, id string) (ProducerAccessStatus, error) {
	store, err := s.producerLifecycleOperations()
	if err != nil {
		return ProducerAccessStatus{}, err
	}
	if !validProducerID(id) {
		return ProducerAccessStatus{}, ErrInvalid
	}
	return store.producerAccessStatus(ctx, s.producerCredentialConfig(id))
}

func (s *Service) ChangeProducerAccess(ctx context.Context, action string, input ProducerLifecycleRequest) (OperationRecord, error) {
	store, err := s.producerLifecycleOperations()
	if err != nil {
		return OperationRecord{}, err
	}
	if input.Validate(action) != nil {
		return OperationRecord{}, ErrInvalid
	}
	return store.changeProducerAccess(ctx, action, input, s.producerCredentialConfig(input.ProducerID), func() bool {
		return s.producerLifecycleEnabled() && s.operationsEnabled()
	})
}
