package managedpostgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
)

type BindingServiceOptions struct {
	LeaseDuration       time.Duration
	ProviderTimeout     time.Duration
	ProvisioningEnabled func() bool
	Now                 func() time.Time
	NewID               func() string
	NewLeaseToken       func() string
}

// BindingService owns the recoverable saga between a provider credential and
// the encrypted app-secret row that exposes it to a workload. Provider calls
// and secret-store writes are deliberately separate: deterministic identities,
// idempotent sink references, and the persisted lease make every boundary safe
// to retry after a process crash.
type BindingService struct {
	registry            *Registry
	databases           Store
	bindings            BindingStore
	sink                CredentialSink
	leaseDuration       time.Duration
	providerTimeout     time.Duration
	provisioningEnabled func() bool
	now                 func() time.Time
	newID               func() string
	newLeaseToken       func() string
}

type CreateBindingRequest struct {
	AccountID      string
	DatabaseID     string
	AppID          string
	Scope          string
	EnvironmentKey string
	Access         CredentialAccess
}

func NewBindingService(registry *Registry, databases Store, bindings BindingStore, sink CredentialSink, options BindingServiceOptions) (*BindingService, error) {
	if registry == nil || databases == nil || bindings == nil || sink == nil {
		return nil, ErrInvalid
	}
	if options.LeaseDuration == 0 {
		options.LeaseDuration = defaultLeaseDuration
	}
	if options.ProviderTimeout == 0 {
		options.ProviderTimeout = defaultProviderTimeout
	}
	if options.ProvisioningEnabled == nil {
		options.ProvisioningEnabled = func() bool { return false }
	}
	if options.LeaseDuration < time.Second || options.ProviderTimeout < time.Second {
		return nil, ErrInvalid
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	if options.NewID == nil {
		options.NewID = uuid.NewString
	}
	if options.NewLeaseToken == nil {
		options.NewLeaseToken = uuid.NewString
	}
	return &BindingService{
		registry:            registry,
		databases:           databases,
		bindings:            bindings,
		sink:                sink,
		leaseDuration:       options.LeaseDuration,
		providerTimeout:     options.ProviderTimeout,
		provisioningEnabled: options.ProvisioningEnabled,
		now:                 options.Now,
		newID:               options.NewID,
		newLeaseToken:       options.NewLeaseToken,
	}, nil
}

func (s *BindingService) Create(ctx context.Context, request CreateBindingRequest) (Binding, error) {
	if !s.provisioningEnabled() {
		return Binding{}, ErrUnavailable
	}
	if request.AccountID == "" || request.DatabaseID == "" || request.AppID == "" ||
		!validBindingScope(request.Scope) || !validEnvironmentKey(request.EnvironmentKey) ||
		(request.Access != CredentialReadWrite && request.Access != CredentialReadOnly) {
		return Binding{}, ErrInvalid
	}
	now := s.now()
	binding, _, err := s.bindings.ReserveBinding(ctx, Binding{
		ID:                   s.newID(),
		AccountID:            request.AccountID,
		DatabaseID:           request.DatabaseID,
		AppID:                request.AppID,
		Scope:                request.Scope,
		EnvironmentKey:       request.EnvironmentKey,
		Access:               request.Access,
		CredentialGeneration: 1,
		State:                BindingStateProvisioning,
		RetryAt:              now,
		CreatedAt:            now,
		UpdatedAt:            now,
	})
	if err != nil {
		return Binding{}, err
	}
	if binding.DatabaseID != request.DatabaseID || binding.AppID != request.AppID ||
		binding.Scope != request.Scope || binding.EnvironmentKey != request.EnvironmentKey || binding.Access != request.Access {
		return Binding{}, ErrConflict
	}
	if binding.State == BindingStateReady {
		return binding, nil
	}
	if binding.State == BindingStateDeleting || binding.State == BindingStateDeleted {
		return Binding{}, ErrConflict
	}
	return s.Reconcile(ctx, request.AccountID, binding.ID)
}

func (s *BindingService) Reconcile(ctx context.Context, accountID, bindingID string) (Binding, error) {
	binding, err := s.bindings.GetBinding(ctx, accountID, bindingID)
	if err != nil {
		return Binding{}, err
	}
	switch binding.State {
	case BindingStateReady:
		return binding, nil
	case BindingStateProvisioning, BindingStateFailed:
		if !s.provisioningEnabled() {
			return Binding{}, ErrUnavailable
		}
	case BindingStateDeleting, BindingStateDeleted:
		return Binding{}, ErrConflict
	default:
		return Binding{}, ErrConflict
	}

	now := s.now()
	binding, err = s.bindings.ClaimBinding(ctx, accountID, bindingID, s.newLeaseToken(), BindingStateProvisioning, now, now.Add(s.leaseDuration))
	if err != nil {
		return Binding{}, err
	}
	database, err := s.databases.Get(ctx, accountID, binding.DatabaseID)
	if err != nil {
		return Binding{}, s.releaseKnownError(ctx, binding, BindingStateFailed, "database_unavailable", normalizeProviderError(err), time.Hour)
	}
	if database.State != StateReady || database.ProviderResourceID == "" {
		return Binding{}, s.releaseKnownError(ctx, binding, BindingStateFailed, "database_not_ready", ErrConflict, time.Hour)
	}
	backend, err := s.registry.Resolve(database.BackendID, database.BackendFingerprint)
	if err != nil {
		return Binding{}, s.releaseKnownError(ctx, binding, BindingStateFailed, "backend_unavailable", ErrUnavailable, time.Hour)
	}

	credentialRequest := bindingCredentialRequest(binding, database.ProviderResourceID)
	providerContext, cancel := context.WithTimeout(ctx, s.providerTimeout)
	material, err := backend.Provider.IssueCredentials(providerContext, credentialRequest)
	cancel()
	if err != nil {
		return Binding{}, s.releaseProviderError(ctx, binding, BindingStateFailed, "credential_issue", err)
	}
	if err := material.Validate(); err != nil {
		clearCredentialMaterial(&material)
		return Binding{}, s.releaseKnownError(ctx, binding, BindingStateFailed, "credential_invalid", ErrUnavailable, time.Hour)
	}
	defer clearCredentialMaterial(&material)

	credentialRef, err := s.putCredential(ctx, binding, material)
	if err != nil {
		normalized := normalizeProviderError(err)
		delay := retryDelay(normalized, binding.AttemptCount)
		if errors.Is(normalized, ErrConflict) || errors.Is(normalized, ErrInvalid) {
			delay = time.Hour
		}
		return Binding{}, s.releaseKnownError(ctx, binding, BindingStateFailed, "secret_write_failed", normalized, delay)
	}
	if !validOpaqueID(credentialRef) {
		return Binding{}, s.releaseKnownError(ctx, binding, BindingStateFailed, "secret_ref_invalid", ErrUnavailable, time.Hour)
	}
	return s.finishProvision(ctx, binding, material.ProviderIdentityID, credentialRef)
}

func (s *BindingService) Delete(ctx context.Context, accountID, bindingID string) (Binding, error) {
	binding, err := s.bindings.GetBinding(ctx, accountID, bindingID)
	if err != nil {
		return Binding{}, err
	}
	if binding.State == BindingStateDeleted {
		return binding, nil
	}
	now := s.now()
	binding, err = s.bindings.ClaimBinding(ctx, accountID, bindingID, s.newLeaseToken(), BindingStateDeleting, now, now.Add(s.leaseDuration))
	if err != nil {
		return Binding{}, err
	}
	database, err := s.databases.Get(ctx, accountID, binding.DatabaseID)
	if err != nil {
		return Binding{}, s.releaseKnownError(ctx, binding, BindingStateDeleting, "database_unavailable", normalizeProviderError(err), time.Hour)
	}
	backend, err := s.registry.Resolve(database.BackendID, database.BackendFingerprint)
	if err != nil {
		return Binding{}, s.releaseKnownError(ctx, binding, BindingStateDeleting, "backend_unavailable", ErrUnavailable, time.Hour)
	}
	if database.ProviderResourceID == "" {
		return Binding{}, s.releaseKnownError(ctx, binding, BindingStateDeleting, "database_not_ready", ErrConflict, time.Hour)
	}

	credentialRequest := bindingCredentialRequest(binding, database.ProviderResourceID)
	providerContext, cancel := context.WithTimeout(ctx, s.providerTimeout)
	err = backend.Provider.RevokeCredentials(providerContext, credentialRequest)
	cancel()
	if err != nil && !errors.Is(err, ErrNotFound) {
		return Binding{}, s.releaseProviderError(ctx, binding, BindingStateDeleting, "credential_revoke", err)
	}
	if err := s.deleteCredential(ctx, binding); err != nil {
		normalized := normalizeProviderError(err)
		delay := retryDelay(normalized, binding.AttemptCount)
		if errors.Is(normalized, ErrConflict) || errors.Is(normalized, ErrInvalid) {
			delay = time.Hour
		}
		return Binding{}, s.releaseKnownError(ctx, binding, BindingStateDeleting, "secret_delete_failed", normalized, delay)
	}
	return s.finishDelete(ctx, binding)
}

func (s *BindingService) Get(ctx context.Context, accountID, bindingID string) (Binding, error) {
	return s.bindings.GetBinding(ctx, accountID, bindingID)
}

func (s *BindingService) List(ctx context.Context, accountID, databaseID string) ([]Binding, error) {
	if accountID == "" || databaseID == "" {
		return nil, ErrInvalid
	}
	return s.bindings.ListBindings(ctx, accountID, databaseID)
}

func (s *BindingService) releaseProviderError(ctx context.Context, binding Binding, next BindingState, stage string, providerErr error) error {
	normalized := normalizeProviderError(providerErr)
	code := stage + "_" + providerErrorCode(providerErr)
	return s.releaseKnownError(ctx, binding, next, code, normalized, retryDelay(normalized, binding.AttemptCount))
}

func (s *BindingService) releaseKnownError(ctx context.Context, binding Binding, next BindingState, code string, operationErr error, delay time.Duration) error {
	now := s.now()
	finishContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultStoreTimeout)
	defer cancel()
	if err := s.bindings.ReleaseBinding(finishContext, binding.ID, binding.LeaseToken, next, code, now, now.Add(delay)); err != nil {
		return errors.Join(operationErr, fmt.Errorf("release managed postgres binding lease: %w", err))
	}
	return operationErr
}

func (s *BindingService) putCredential(ctx context.Context, binding Binding, material CredentialMaterial) (string, error) {
	finishContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultStoreTimeout)
	defer cancel()
	return s.sink.Put(finishContext, binding, material)
}

func (s *BindingService) deleteCredential(ctx context.Context, binding Binding) error {
	finishContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultStoreTimeout)
	defer cancel()
	return s.sink.Delete(finishContext, binding)
}

func (s *BindingService) finishProvision(ctx context.Context, binding Binding, providerIdentityID, credentialRef string) (Binding, error) {
	finishContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultStoreTimeout)
	defer cancel()
	return s.bindings.FinishBindingProvision(finishContext, binding.ID, binding.LeaseToken, providerIdentityID, credentialRef, s.now())
}

func (s *BindingService) finishDelete(ctx context.Context, binding Binding) (Binding, error) {
	finishContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultStoreTimeout)
	defer cancel()
	return s.bindings.FinishBindingDelete(finishContext, binding.ID, binding.LeaseToken, s.now())
}

func bindingCredentialRequest(binding Binding, providerResourceID string) CredentialRequest {
	identityKey := bindingCredentialIdentity(binding)
	return CredentialRequest{
		ProviderResourceID: providerResourceID,
		IdentityKey:        identityKey,
		Access:             binding.Access,
		IdempotencyKey:     "credentials-" + identityKey,
	}
}

func bindingCredentialIdentity(binding Binding) string {
	sum := sha256.Sum256([]byte(binding.ID + "\x00" + strconv.FormatInt(binding.CredentialGeneration, 10)))
	return "gregale-binding-" + hex.EncodeToString(sum[:20])
}

func clearCredentialMaterial(material *CredentialMaterial) {
	if material == nil {
		return
	}
	material.Username = ""
	material.ProviderIdentityID = ""
	material.Password = ""
	material.Database = ""
	material.TLSMode = ""
	material.RootCertificatePEM = ""
	material.Endpoints = nil
}
