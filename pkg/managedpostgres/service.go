package managedpostgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

const (
	defaultLeaseDuration   = 2 * time.Minute
	defaultProviderTimeout = 30 * time.Second
	defaultPollInterval    = 15 * time.Second
	defaultStoreTimeout    = 5 * time.Second
)

type ServiceOptions struct {
	LeaseDuration   time.Duration
	ProviderTimeout time.Duration
	PollInterval    time.Duration
	Now             func() time.Time
	NewID           func() string
	NewLeaseToken   func() string
}

type Service struct {
	registry        *Registry
	store           Store
	leaseDuration   time.Duration
	providerTimeout time.Duration
	pollInterval    time.Duration
	now             func() time.Time
	newID           func() string
	newLeaseToken   func() string
}

type CreateRequest struct {
	AccountID string
	Name      string
	Spec      Spec
}

func NewService(registry *Registry, store Store, options ServiceOptions) (*Service, error) {
	if registry == nil || store == nil {
		return nil, ErrInvalid
	}
	if options.LeaseDuration == 0 {
		options.LeaseDuration = defaultLeaseDuration
	}
	if options.ProviderTimeout == 0 {
		options.ProviderTimeout = defaultProviderTimeout
	}
	if options.PollInterval == 0 {
		options.PollInterval = defaultPollInterval
	}
	if options.LeaseDuration < time.Second || options.ProviderTimeout < time.Second || options.PollInterval < time.Second {
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
	return &Service{
		registry:        registry,
		store:           store,
		leaseDuration:   options.LeaseDuration,
		providerTimeout: options.ProviderTimeout,
		pollInterval:    options.PollInterval,
		now:             options.Now,
		newID:           options.NewID,
		newLeaseToken:   options.NewLeaseToken,
	}, nil
}

func (s *Service) Create(ctx context.Context, request CreateRequest) (Database, error) {
	if request.AccountID == "" || !ValidName(request.Name) {
		return Database{}, ErrInvalid
	}
	if err := request.Spec.Validate(); err != nil {
		return Database{}, err
	}
	existing, err := s.store.FindByName(ctx, request.AccountID, request.Name)
	if err == nil {
		if existing.Spec != request.Spec {
			return Database{}, ErrConflict
		}
		if existing.State == StateReady {
			return existing, nil
		}
		return s.Reconcile(ctx, request.AccountID, existing.ID)
	}
	if !errors.Is(err, ErrNotFound) {
		return Database{}, err
	}
	backend, err := s.registry.Default(request.Spec.Region)
	if err != nil {
		return Database{}, err
	}
	if err := backend.Capabilities.Supports(request.Spec); err != nil {
		return Database{}, err
	}
	now := s.now()
	database, _, err := s.store.Reserve(ctx, Database{
		ID:                 s.newID(),
		AccountID:          request.AccountID,
		Name:               request.Name,
		Spec:               request.Spec,
		BackendID:          backend.ID,
		BackendFingerprint: backend.Fingerprint,
		State:              StateProvisioning,
		DesiredGeneration:  1,
		CreatedAt:          now,
		UpdatedAt:          now,
	}, s.registry.MaxDatabasesPerAccount)
	if err != nil {
		return Database{}, err
	}
	if database.Spec != request.Spec {
		return Database{}, ErrConflict
	}
	if database.State == StateReady {
		return database, nil
	}
	return s.Reconcile(ctx, request.AccountID, database.ID)
}

func (s *Service) Reconcile(ctx context.Context, accountID, databaseID string) (Database, error) {
	database, err := s.store.Get(ctx, accountID, databaseID)
	if err != nil {
		return Database{}, err
	}
	switch database.State {
	case StateReady:
		return database, nil
	case StateDeleting, StateDeleted:
		return Database{}, ErrConflict
	case StateProvisioning, StateFailed:
	default:
		return Database{}, ErrConflict
	}
	now := s.now()
	leaseToken := s.newLeaseToken()
	database, err = s.store.Claim(ctx, accountID, databaseID, leaseToken, StateProvisioning, now, now.Add(s.leaseDuration))
	if err != nil {
		return Database{}, err
	}
	backend, err := s.registry.Resolve(database.BackendID, database.BackendFingerprint)
	if err != nil {
		return Database{}, s.releaseKnownError(ctx, database, StateFailed, "backend_unavailable", ErrUnavailable, time.Hour)
	}
	if err := backend.Capabilities.Supports(database.Spec); err != nil {
		return Database{}, s.releaseKnownError(ctx, database, StateFailed, "unsupported", err, time.Hour)
	}
	providerContext, cancel := context.WithTimeout(ctx, s.providerTimeout)
	defer cancel()
	var observed ObservedDatabase
	if database.ProviderResourceID == "" {
		observed, err = backend.Provider.Provision(providerContext, ProvisionRequest{
			ResourceID:     database.ID,
			Spec:           database.Spec,
			IdempotencyKey: "provision-" + database.ID,
		})
		if err == nil {
			if observed.ProviderResourceID == "" {
				err = ErrUnavailable
			} else {
				err = s.recordProviderResource(ctx, database.ID, leaseToken, observed.ProviderResourceID)
				database.ProviderResourceID = observed.ProviderResourceID
			}
		}
	} else {
		observed, err = backend.Provider.Inspect(providerContext, database.ProviderResourceID)
		if errors.Is(err, ErrNotFound) {
			// The Gregale resource still exists; an upstream disappearance is
			// an availability incident, not a customer-facing 404.
			err = ErrUnavailable
		}
		if err == nil && observed.ProviderResourceID != database.ProviderResourceID {
			err = ErrUnavailable
		}
	}
	if err != nil {
		return Database{}, s.releaseProviderError(ctx, database, StateProvisioning, err)
	}
	switch observed.Status {
	case ProviderStatusPending, ProviderStatusDeleting:
		if err := s.release(ctx, database.ID, leaseToken, StateProvisioning, "", s.pollInterval); err != nil {
			return Database{}, err
		}
		return s.store.Get(ctx, accountID, databaseID)
	case ProviderStatusReady:
		if observed.Spec != database.Spec {
			return Database{}, s.releaseKnownError(ctx, database, StateFailed, "spec_mismatch", ErrConflict, time.Hour)
		}
		return s.finishProvision(ctx, database.ID, leaseToken)
	case ProviderStatusFailed:
		return Database{}, s.releaseKnownError(ctx, database, StateFailed, "provider_failed", ErrUnavailable, time.Hour)
	default:
		return Database{}, s.releaseProviderError(ctx, database, StateFailed, ErrUnavailable)
	}
}

func (s *Service) Delete(ctx context.Context, accountID, databaseID string) (Database, error) {
	database, err := s.store.Get(ctx, accountID, databaseID)
	if err != nil {
		return Database{}, err
	}
	if database.State == StateDeleted {
		return database, nil
	}
	now := s.now()
	leaseToken := s.newLeaseToken()
	database, err = s.store.Claim(ctx, accountID, databaseID, leaseToken, StateDeleting, now, now.Add(s.leaseDuration))
	if err != nil {
		return Database{}, err
	}
	if database.ProviderResourceID == "" {
		return s.finishDelete(ctx, database.ID, leaseToken)
	}
	backend, err := s.registry.Resolve(database.BackendID, database.BackendFingerprint)
	if err != nil {
		return Database{}, s.releaseKnownError(ctx, database, StateDeleting, "backend_unavailable", ErrUnavailable, time.Hour)
	}
	providerContext, cancel := context.WithTimeout(ctx, s.providerTimeout)
	defer cancel()
	result, err := backend.Provider.Delete(providerContext, DeleteRequest{
		ProviderResourceID: database.ProviderResourceID,
		IdempotencyKey:     "delete-" + database.ID,
	})
	if errors.Is(err, ErrNotFound) {
		result.Done = true
		err = nil
	}
	if err != nil {
		return Database{}, s.releaseProviderError(ctx, database, StateDeleting, err)
	}
	if !result.Done {
		if err := s.release(ctx, database.ID, leaseToken, StateDeleting, "", s.pollInterval); err != nil {
			return Database{}, err
		}
		return s.store.Get(ctx, accountID, databaseID)
	}
	return s.finishDelete(ctx, database.ID, leaseToken)
}

func (s *Service) Get(ctx context.Context, accountID, databaseID string) (Database, error) {
	return s.store.Get(ctx, accountID, databaseID)
}

func (s *Service) List(ctx context.Context, accountID string) ([]Database, error) {
	if accountID == "" {
		return nil, ErrInvalid
	}
	return s.store.List(ctx, accountID)
}

func (s *Service) releaseProviderError(ctx context.Context, database Database, next State, providerErr error) error {
	normalized := normalizeProviderError(providerErr)
	return s.releaseKnownError(ctx, database, next, providerErrorCode(providerErr), normalized, retryDelay(normalized, database.AttemptCount))
}

func (s *Service) releaseKnownError(ctx context.Context, database Database, next State, code string, operationErr error, delay time.Duration) error {
	if err := s.release(ctx, database.ID, database.LeaseToken, next, code, delay); err != nil {
		return errors.Join(operationErr, fmt.Errorf("release managed postgres lease: %w", err))
	}
	return operationErr
}

func (s *Service) release(ctx context.Context, databaseID, leaseToken string, next State, code string, delay time.Duration) error {
	now := s.now()
	finishContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultStoreTimeout)
	defer cancel()
	return s.store.Release(finishContext, databaseID, leaseToken, next, code, now, now.Add(delay))
}

func (s *Service) recordProviderResource(ctx context.Context, databaseID, leaseToken, providerResourceID string) error {
	finishContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultStoreTimeout)
	defer cancel()
	return s.store.RecordProviderResource(finishContext, databaseID, leaseToken, providerResourceID, s.now())
}

func (s *Service) finishProvision(ctx context.Context, databaseID, leaseToken string) (Database, error) {
	finishContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultStoreTimeout)
	defer cancel()
	return s.store.FinishProvision(finishContext, databaseID, leaseToken, s.now())
}

func (s *Service) finishDelete(ctx context.Context, databaseID, leaseToken string) (Database, error) {
	finishContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultStoreTimeout)
	defer cancel()
	return s.store.FinishDelete(finishContext, databaseID, leaseToken, s.now())
}

func retryDelay(err error, attempt int32) time.Duration {
	if errors.Is(err, ErrInvalid) || errors.Is(err, ErrUnsupported) || errors.Is(err, ErrQuotaExceeded) {
		return time.Hour
	}
	delay := 30 * time.Second
	for i := int32(1); i < attempt && delay < 15*time.Minute; i++ {
		delay *= 2
	}
	return min(delay, 15*time.Minute)
}
