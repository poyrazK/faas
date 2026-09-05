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
)

type ServiceOptions struct {
	LeaseDuration   time.Duration
	ProviderTimeout time.Duration
	Now             func() time.Time
	NewID           func() string
	NewLeaseToken   func() string
}

type Service struct {
	registry        *Registry
	store           Store
	leaseDuration   time.Duration
	providerTimeout time.Duration
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
	return &Service{
		registry:        registry,
		store:           store,
		leaseDuration:   options.LeaseDuration,
		providerTimeout: options.ProviderTimeout,
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
	backend, err := s.registry.Resolve(database.BackendID, database.BackendFingerprint)
	if err != nil {
		return Database{}, err
	}
	if err := backend.Capabilities.Supports(database.Spec); err != nil {
		return Database{}, err
	}
	now := s.now()
	leaseToken := s.newLeaseToken()
	database, err = s.store.Claim(ctx, accountID, databaseID, leaseToken, StateProvisioning, now, now.Add(s.leaseDuration))
	if err != nil {
		return Database{}, err
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
				err = s.store.RecordProviderResource(ctx, database.ID, leaseToken, observed.ProviderResourceID, s.now())
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
		return Database{}, s.releaseProviderError(ctx, database.ID, leaseToken, StateProvisioning, err)
	}
	switch observed.Status {
	case ProviderStatusPending, ProviderStatusDeleting:
		if err := s.store.Release(ctx, database.ID, leaseToken, StateProvisioning, "", s.now()); err != nil {
			return Database{}, err
		}
		return s.store.Get(ctx, accountID, databaseID)
	case ProviderStatusReady:
		if observed.Spec != database.Spec {
			if releaseErr := s.store.Release(ctx, database.ID, leaseToken, StateFailed, "spec_mismatch", s.now()); releaseErr != nil {
				return Database{}, releaseErr
			}
			return Database{}, ErrConflict
		}
		return s.store.FinishProvision(ctx, database.ID, leaseToken, s.now())
	case ProviderStatusFailed:
		if releaseErr := s.store.Release(ctx, database.ID, leaseToken, StateFailed, "provider_failed", s.now()); releaseErr != nil {
			return Database{}, releaseErr
		}
		return Database{}, ErrUnavailable
	default:
		return Database{}, s.releaseProviderError(ctx, database.ID, leaseToken, StateFailed, ErrUnavailable)
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
		return s.store.FinishDelete(ctx, database.ID, leaseToken, s.now())
	}
	backend, err := s.registry.Resolve(database.BackendID, database.BackendFingerprint)
	if err != nil {
		if releaseErr := s.store.Release(ctx, database.ID, leaseToken, StateDeleting, "backend_unavailable", s.now()); releaseErr != nil {
			return Database{}, errors.Join(err, releaseErr)
		}
		return Database{}, err
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
		return Database{}, s.releaseProviderError(ctx, database.ID, leaseToken, StateDeleting, err)
	}
	if !result.Done {
		if err := s.store.Release(ctx, database.ID, leaseToken, StateDeleting, "", s.now()); err != nil {
			return Database{}, err
		}
		return s.store.Get(ctx, accountID, databaseID)
	}
	return s.store.FinishDelete(ctx, database.ID, leaseToken, s.now())
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

func (s *Service) releaseProviderError(ctx context.Context, databaseID, leaseToken string, next State, providerErr error) error {
	normalized := normalizeProviderError(providerErr)
	if err := s.store.Release(ctx, databaseID, leaseToken, next, providerErrorCode(providerErr), s.now()); err != nil {
		return errors.Join(normalized, fmt.Errorf("release managed postgres lease: %w", err))
	}
	return normalized
}
