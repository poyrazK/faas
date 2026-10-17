package internalmail

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"strings"

	"github.com/google/uuid"
)

func (s *Service) newDKIMKey(domainID string) (DKIMKey, []byte, error) {
	if !s.rotationEnabled() || !s.operationsEnabled() {
		return DKIMKey{}, nil, ErrDisabled
	}
	if s.Codec == nil {
		return DKIMKey{}, nil, ErrInvalid
	}
	id := uuid.NewString()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return DKIMKey{}, nil, err
	}
	public, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return DKIMKey{}, nil, err
	}
	plain := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	defer clear(plain)
	if !s.rotationEnabled() || !s.operationsEnabled() {
		return DKIMKey{}, nil, ErrDisabled
	}
	sealed, err := s.Codec.Seal(dkimKeyResource(domainID, id), plain)
	return DKIMKey{ID: id, Selector: "gregale" + strings.ReplaceAll(id, "-", ""), PublicKey: base64.StdEncoding.EncodeToString(public)}, sealed, err
}

func (s *Service) StageDKIMKey(ctx context.Context, id string, input OperatorAuditInput) (OperationRecord, error) {
	store, err := s.lifecycleStore(true, true)
	if err != nil {
		return OperationRecord{}, err
	}
	if !canonicalOperatorID(id) || input.Validate() != nil {
		return OperationRecord{}, ErrInvalid
	}
	return store.stageDKIM(ctx, id, input, func() (DKIMKey, []byte, error) { return s.newDKIMKey(id) }, func() bool { return s.rotationEnabled() && s.operationsEnabled() })
}

func dkimKeyResource(domainID, keyID string) string { return "domain:" + domainID + ":dkim:" + keyID }
func (s *Service) VerifyDKIMKey(ctx context.Context, id, keyID string, input OperatorAuditInput) (OperationRecord, error) {
	store, err := s.lifecycleStore(true, true)
	if err != nil {
		return OperationRecord{}, err
	}
	if !canonicalOperatorID(id) || !canonicalOperatorID(keyID) || input.Validate() != nil {
		return OperationRecord{}, ErrInvalid
	}
	return store.verifyDKIM(ctx, id, keyID, input, func(ctx context.Context, key DKIMKey) (string, error) {
		return s.observeRecords(ctx, Domain{}, []DNSRecord{key.Record}, []int{1})
	}, func() bool { return s.rotationEnabled() && s.operationsEnabled() })
}
func (s *Service) ActivateDKIMKey(ctx context.Context, id, keyID string, input OperatorAuditInput) (OperationRecord, error) {
	store, err := s.lifecycleStore(true, true)
	if err != nil {
		return OperationRecord{}, err
	}
	if !canonicalOperatorID(id) || !canonicalOperatorID(keyID) || input.Validate() != nil {
		return OperationRecord{}, ErrInvalid
	}
	return store.activateDKIM(ctx, id, keyID, input, s.observeDomain, s.SendingIP.String(), func() bool { return s.rotationEnabled() && s.operationsEnabled() })
}
func (s *Service) RetireDKIMKey(ctx context.Context, id, keyID string, input OperatorAuditInput, queueDrained bool) (OperationRecord, error) {
	store, err := s.lifecycleStore(true, true)
	if err != nil {
		return OperationRecord{}, err
	}
	if !canonicalOperatorID(id) || !canonicalOperatorID(keyID) || input.Validate() != nil || !queueDrained {
		return OperationRecord{}, ErrInvalid
	}
	return store.retireDKIM(ctx, id, keyID, input, func() bool { return s.rotationEnabled() && s.operationsEnabled() })
}
func (s *Service) ReverifyDomain(ctx context.Context, id string, input OperatorAuditInput) (OperationRecord, error) {
	store, err := s.lifecycleStore(false, true)
	if err != nil {
		return OperationRecord{}, err
	}
	if !canonicalOperatorID(id) || input.Validate() != nil {
		return OperationRecord{}, ErrInvalid
	}
	return store.operatorReverify(ctx, id, input, s.observeDomain, s.SendingIP.String(), func() bool { return s.reverificationEnabled() && s.operationsEnabled() })
}
