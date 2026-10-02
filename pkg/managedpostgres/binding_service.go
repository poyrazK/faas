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
	RetryInterval       time.Duration
	ProvisioningEnabled func() bool
	// ProvisioningAllowed optionally narrows an enabled rollout to specific
	// accounts. It is intended for staging canaries; deletion remains
	// available regardless of this gate.
	ProvisioningAllowed func(context.Context, string) bool
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
	retryInterval       time.Duration
	provisioningEnabled func() bool
	provisioningAllowed func(context.Context, string) bool
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
	if options.RetryInterval == 0 {
		options.RetryInterval = defaultPollInterval
	}
	if options.ProvisioningEnabled == nil {
		options.ProvisioningEnabled = func() bool { return false }
	}
	if options.ProvisioningAllowed == nil {
		options.ProvisioningAllowed = func(context.Context, string) bool { return true }
	}
	if options.LeaseDuration < time.Second || options.ProviderTimeout < time.Second || options.RetryInterval < time.Second {
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
		retryInterval:       options.RetryInterval,
		provisioningEnabled: options.ProvisioningEnabled,
		provisioningAllowed: options.ProvisioningAllowed,
		now:                 options.Now,
		newID:               options.NewID,
		newLeaseToken:       options.NewLeaseToken,
	}, nil
}

func (s *BindingService) Create(ctx context.Context, request CreateBindingRequest) (Binding, error) {
	binding, _, err := s.CreateWithResult(ctx, request)
	return binding, err
}

// CreateWithResult is the same idempotent binding operation as Create, but
// also reports whether this invocation reserved a new binding row. Deploy
// orchestration uses that bit to compensate only rows it created when a
// later manifest dependency fails; pre-existing customer bindings are never
// deleted during rollback.
func (s *BindingService) CreateWithResult(ctx context.Context, request CreateBindingRequest) (Binding, bool, error) {
	if !s.provisioningEnabled() {
		return Binding{}, false, ErrUnavailable
	}
	if request.AccountID == "" || request.DatabaseID == "" || request.AppID == "" ||
		!validBindingScope(request.Scope) || !validEnvironmentKey(request.EnvironmentKey) ||
		(request.Access != CredentialReadWrite && request.Access != CredentialReadOnly) {
		return Binding{}, false, ErrInvalid
	}
	if !s.provisioningAllowed(ctx, request.AccountID) {
		return Binding{}, false, ErrUnavailable
	}
	database, err := customerDatabase(ctx, s.databases, request.AccountID, request.DatabaseID)
	if err != nil {
		return Binding{}, false, err
	}
	backend, err := s.registry.Resolve(database.BackendID, database.BackendFingerprint)
	if err != nil {
		return Binding{}, false, err
	}
	if err := backend.Capabilities.SupportsCredentialAccess(request.Access); err != nil {
		return Binding{}, false, err
	}
	now := s.now()
	binding, created, err := s.bindings.ReserveBinding(ctx, Binding{
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
		return Binding{}, false, err
	}
	if binding.DatabaseID != request.DatabaseID || binding.AppID != request.AppID ||
		binding.Scope != request.Scope || binding.EnvironmentKey != request.EnvironmentKey || binding.Access != request.Access {
		return Binding{}, false, ErrConflict
	}
	if binding.State == BindingStateReady {
		return binding, created, nil
	}
	if binding.State == BindingStateDeleting || binding.State == BindingStateDeleted {
		return Binding{}, false, ErrConflict
	}
	// A database may still be provisioning. Reserve the durable binding now so
	// the binding reconciler can inject DATABASE_URL as soon as it is ready.
	if database.State == StateProvisioning {
		return binding, created, nil
	}
	ready, err := s.Reconcile(ctx, request.AccountID, binding.ID)
	if err != nil {
		// Preserve the reserved row identity so callers can compensate a
		// failed multi-binding operation without guessing which row was
		// created. The legacy Create wrapper still exposes only the error.
		return binding, created, err
	}
	return ready, created, nil
}

// Rotate creates a new provider identity and replaces the managed app secret.
// The previous generation remains valid until the scheduler confirms that a
// rolling runtime refresh has completed; retries reuse the persisted wake ID
// and generation rather than creating another credential.
func (s *BindingService) Rotate(ctx context.Context, accountID, bindingID string) (Binding, error) {
	if !s.provisioningEnabled() || !s.provisioningAllowed(ctx, accountID) {
		return Binding{}, ErrUnavailable
	}
	binding, err := s.Get(ctx, accountID, bindingID)
	if err != nil {
		return Binding{}, err
	}
	if binding.RotationPreviousGeneration == 0 {
		if binding.State != BindingStateReady {
			return Binding{}, ErrConflict
		}
		binding, _, err = s.bindings.BeginBindingRotation(ctx, accountID, bindingID, s.newID(), s.now())
		if err != nil {
			return Binding{}, err
		}
	}
	if binding.RotationPreviousGeneration < 1 || binding.RotationWakeID == "" {
		return Binding{}, ErrConflict
	}
	switch binding.State {
	case BindingStateProvisioning, BindingStateFailed:
		return s.Reconcile(ctx, accountID, bindingID)
	case BindingStateReady, BindingStateRetiring:
		return binding, nil
	default:
		return Binding{}, ErrConflict
	}
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
		if !s.provisioningEnabled() || !s.provisioningAllowed(ctx, accountID) {
			return Binding{}, ErrUnavailable
		}
	case BindingStateDeleting, BindingStateRetiring, BindingStateDeleted:
		return Binding{}, ErrConflict
	default:
		return Binding{}, ErrConflict
	}
	// A private clone binding is prepared by its owning worker and atomic
	// receipt sink. Ordinary reconciliation must not issue credentials or
	// invalidate the source app's runtime configuration while it is pending.
	if _, err := customerDatabase(ctx, s.databases, accountID, binding.DatabaseID); err != nil {
		return Binding{}, err
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
	if database.State == StateProvisioning {
		return Binding{}, s.releaseKnownError(ctx, binding, BindingStateProvisioning, "database_not_ready", ErrConflict, s.retryInterval)
	}
	if database.State != StateReady || database.ProviderResourceID == "" {
		return Binding{}, s.releaseKnownError(ctx, binding, BindingStateFailed, "database_not_ready", ErrConflict, time.Hour)
	}
	backend, err := s.registry.Resolve(database.BackendID, database.BackendFingerprint)
	if err != nil {
		return Binding{}, s.releaseKnownError(ctx, binding, BindingStateFailed, "backend_unavailable", ErrUnavailable, time.Hour)
	}
	if err := backend.Capabilities.SupportsCredentialAccess(binding.Access); err != nil {
		return Binding{}, s.releaseKnownError(ctx, binding, BindingStateFailed, "credential_access_unsupported", err, time.Hour)
	}

	credentialRequest := bindingCredentialRequest(binding, databaseDataResource(database))
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
	binding, err := s.Get(ctx, accountID, bindingID)
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
		// No provider credential can exist before the database has a provider
		// resource. Finish the durable binding deletion locally.
		return s.finishDelete(ctx, binding)
	}
	if database.State == StateProvisioning {
		return Binding{}, s.releaseKnownError(ctx, binding, BindingStateDeleting, "database_not_ready", ErrConflict, time.Hour)
	}

	for _, generation := range []int64{binding.CredentialGeneration, binding.RotationPreviousGeneration} {
		if generation < 1 {
			continue
		}
		credentialRequest := bindingCredentialRequestForGeneration(binding, databaseDataResource(database), generation)
		providerContext, cancel := context.WithTimeout(ctx, s.providerTimeout)
		err = backend.Provider.RevokeCredentials(providerContext, credentialRequest)
		cancel()
		if err != nil && !errors.Is(err, ErrNotFound) {
			return Binding{}, s.releaseProviderError(ctx, binding, BindingStateDeleting, "credential_revoke", err)
		}
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

// ReconcileRotationCleanup retires the old provider identity only after the
// scheduler has marked the rotation wake as delivered. Revoke is idempotent,
// so an expired lease or process crash is safe to retry.
func (s *BindingService) ReconcileRotationCleanup(ctx context.Context, accountID, bindingID string) (Binding, error) {
	binding, err := s.bindings.GetBinding(ctx, accountID, bindingID)
	if err != nil {
		return Binding{}, err
	}
	if binding.RotationPreviousGeneration < 1 || binding.RotationWakeID == "" || !binding.RotationCleanupReady {
		return binding, nil
	}
	now := s.now()
	binding, err = s.bindings.ClaimBinding(ctx, accountID, bindingID, s.newLeaseToken(), BindingStateRetiring, now, now.Add(s.leaseDuration))
	if err != nil {
		return Binding{}, err
	}
	database, err := s.databases.Get(ctx, accountID, binding.DatabaseID)
	if err != nil {
		return Binding{}, s.releaseKnownError(ctx, binding, BindingStateRetiring, "database_unavailable", normalizeProviderError(err), time.Hour)
	}
	if database.ProviderResourceID == "" {
		return s.finishBindingRotationCleanup(ctx, binding)
	}
	backend, err := s.registry.Resolve(database.BackendID, database.BackendFingerprint)
	if err != nil {
		return Binding{}, s.releaseKnownError(ctx, binding, BindingStateRetiring, "backend_unavailable", ErrUnavailable, time.Hour)
	}
	request := bindingCredentialRequestForGeneration(binding, databaseDataResource(database), binding.RotationPreviousGeneration)
	providerContext, cancel := context.WithTimeout(ctx, s.providerTimeout)
	err = backend.Provider.RevokeCredentials(providerContext, request)
	cancel()
	if err != nil && !errors.Is(err, ErrNotFound) {
		return Binding{}, s.releaseProviderError(ctx, binding, BindingStateRetiring, "rotation_revoke", err)
	}
	return s.finishBindingRotationCleanup(ctx, binding)
}

func (s *BindingService) Get(ctx context.Context, accountID, bindingID string) (Binding, error) {
	binding, err := s.bindings.GetBinding(ctx, accountID, bindingID)
	if err != nil {
		return Binding{}, err
	}
	if _, err := customerDatabase(ctx, s.databases, accountID, binding.DatabaseID); err != nil {
		return Binding{}, err
	}
	return binding, nil
}

func (s *BindingService) List(ctx context.Context, accountID, databaseID string) ([]Binding, error) {
	if accountID == "" || databaseID == "" {
		return nil, ErrInvalid
	}
	if _, err := customerDatabase(ctx, s.databases, accountID, databaseID); err != nil {
		return nil, err
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

func (s *BindingService) finishBindingRotationCleanup(ctx context.Context, binding Binding) (Binding, error) {
	finishContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultStoreTimeout)
	defer cancel()
	return s.bindings.FinishBindingRotationCleanup(finishContext, binding.ID, binding.LeaseToken, binding.RotationWakeID, s.now())
}

func bindingCredentialRequest(binding Binding, providerResourceID string) CredentialRequest {
	return bindingCredentialRequestForGeneration(binding, providerResourceID, binding.CredentialGeneration)
}

func bindingCredentialRequestForGeneration(binding Binding, providerResourceID string, generation int64) CredentialRequest {
	binding.CredentialGeneration = generation
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
