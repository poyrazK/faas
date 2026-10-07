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
	LeaseDuration       time.Duration
	ProviderTimeout     time.Duration
	PollInterval        time.Duration
	ProvisioningEnabled func() bool
	// ProvisioningAllowed optionally narrows an enabled rollout to specific
	// accounts. It is intended for staging canaries; deletion remains
	// available regardless of this gate.
	ProvisioningAllowed func(context.Context, string) bool
	Now                 func() time.Time
	NewID               func() string
	NewLeaseToken       func() string
	Admit               func(context.Context, string) error
	AdmitResize         func(context.Context, string, Spec) error
	// MaxDatabasesPerAccount supplies the customer-specific reservation limit.
	// It is evaluated immediately before the store's atomic reservation so
	// plan entitlements remain race-safe while the service stays provider-neutral.
	MaxDatabasesPerAccount func(context.Context, string) (int, error)
}

type Service struct {
	registry               *Registry
	store                  Store
	leaseDuration          time.Duration
	providerTimeout        time.Duration
	pollInterval           time.Duration
	provisioningEnabled    func() bool
	provisioningAllowed    func(context.Context, string) bool
	now                    func() time.Time
	newID                  func() string
	newLeaseToken          func() string
	admit                  func(context.Context, string) error
	admitResize            func(context.Context, string, Spec) error
	maxDatabasesPerAccount func(context.Context, string) (int, error)
}

// DefaultRegion returns the operator-configured placement used when a
// higher-level workflow does not ask for a specific region.
func (s *Service) DefaultRegion() string {
	if s == nil || s.registry == nil {
		return ""
	}
	return s.registry.DefaultRegion
}

type CreateRequest struct {
	AccountID string
	Name      string
	Spec      Spec
}

type RestoreDatabaseRequest struct {
	AccountID        string
	SourceDatabaseID string
	Name             string
	PointInTime      time.Time
	// SourceDefinition is an optional frozen environment-clone input. The
	// source must still have this provider identity, but later desired spec
	// edits cannot replace the captured target configuration.
	SourceDefinition *RestoreSourceDefinition
}

type RestoreSourceDefinition struct {
	Spec                                              Spec
	BackendID, BackendFingerprint, ProviderResourceID string
	DataResourceID                                    string
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
	if options.ProvisioningEnabled == nil {
		options.ProvisioningEnabled = func() bool { return false }
	}
	if options.ProvisioningAllowed == nil {
		options.ProvisioningAllowed = func(context.Context, string) bool { return true }
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
	if options.MaxDatabasesPerAccount == nil {
		options.MaxDatabasesPerAccount = func(context.Context, string) (int, error) {
			return registry.MaxDatabasesPerAccount, nil
		}
	}
	return &Service{
		registry:               registry,
		store:                  store,
		leaseDuration:          options.LeaseDuration,
		providerTimeout:        options.ProviderTimeout,
		pollInterval:           options.PollInterval,
		provisioningEnabled:    options.ProvisioningEnabled,
		provisioningAllowed:    options.ProvisioningAllowed,
		now:                    options.Now,
		newID:                  options.NewID,
		newLeaseToken:          options.NewLeaseToken,
		admit:                  options.Admit,
		admitResize:            options.AdmitResize,
		maxDatabasesPerAccount: options.MaxDatabasesPerAccount,
	}, nil
}

func (s *Service) reservationLimit(ctx context.Context, accountID string) (int, error) {
	limit, err := s.maxDatabasesPerAccount(ctx, accountID)
	if err != nil {
		return 0, err
	}
	if limit < 1 || limit > 100 {
		return 0, ErrInvalid
	}
	return limit, nil
}

func (s *Service) Create(ctx context.Context, request CreateRequest) (Database, error) {
	if !s.provisioningEnabled() {
		return Database{}, ErrUnavailable
	}
	if request.AccountID == "" || !ValidName(request.Name) {
		return Database{}, ErrInvalid
	}
	if err := request.Spec.Validate(); err != nil {
		return Database{}, err
	}
	if !s.provisioningAllowed(ctx, request.AccountID) {
		return Database{}, ErrUnavailable
	}
	existing, err := s.store.FindByName(ctx, request.AccountID, request.Name)
	if err == nil {
		if _, err := s.Get(ctx, request.AccountID, existing.ID); err != nil {
			return Database{}, err
		}
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
	if s.admit != nil {
		if err := s.admit(ctx, request.AccountID); err != nil {
			return Database{}, err
		}
	}
	backend, err := s.registry.Default(request.Spec.Region)
	if err != nil {
		return Database{}, err
	}
	if err := backend.Capabilities.Supports(request.Spec); err != nil {
		return Database{}, err
	}
	now := s.now()
	reservationLimit, err := s.reservationLimit(ctx, request.AccountID)
	if err != nil {
		return Database{}, err
	}
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
	}, reservationLimit)
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

// Restore creates a new database from a ready source without changing the
// source or any existing bindings. The target row stores the source provider
// identity and timestamp before provider I/O, so a worker crash can safely
// resume the same restore intent.
func (s *Service) Restore(ctx context.Context, request RestoreDatabaseRequest) (Database, error) {
	database, _, err := s.RestoreWithResult(ctx, request)
	return database, err
}

// RestoreWithResult also reports whether this invocation reserved the target.
// Callers must compensate only resources they created, never adopted restores.
func (s *Service) RestoreWithResult(ctx context.Context, request RestoreDatabaseRequest) (Database, bool, error) {
	if !s.provisioningEnabled() {
		return Database{}, false, ErrUnavailable
	}
	if request.AccountID == "" || request.SourceDatabaseID == "" || !ValidName(request.Name) || request.PointInTime.IsZero() {
		return Database{}, false, ErrInvalid
	}
	if !s.provisioningAllowed(ctx, request.AccountID) {
		return Database{}, false, ErrUnavailable
	}
	source, err := s.Get(ctx, request.AccountID, request.SourceDatabaseID)
	if err != nil {
		return Database{}, false, err
	}
	if source.State != StateReady || source.ProviderResourceID == "" {
		return Database{}, false, ErrConflict
	}
	now := s.now()
	currentRestoreWindow := source.Spec.RestoreWindowSeconds
	if request.SourceDefinition != nil {
		definition := *request.SourceDefinition
		if definition.Spec.Validate() != nil || definition.Spec.RestoreWindowSeconds <= 0 || definition.BackendID == "" || definition.BackendFingerprint == "" || definition.ProviderResourceID == "" || definition.DataResourceID != "" && !validDataResourceID(definition.DataResourceID) {
			return Database{}, false, ErrInvalid
		}
		if source.BackendID != definition.BackendID || source.BackendFingerprint != definition.BackendFingerprint || source.ProviderResourceID != definition.ProviderResourceID || source.DataResourceID != definition.DataResourceID {
			return Database{}, false, ErrConflict
		}
		source.Spec = definition.Spec
	}
	// Returning an existing restore does not require its original point to
	// remain in retention: the durable target has already been reserved.
	existing, err := s.store.FindByName(ctx, request.AccountID, request.Name)
	if err == nil {
		if _, err := s.Get(ctx, request.AccountID, existing.ID); err != nil {
			return Database{}, false, err
		}
		if existing.RestoreSourceDatabaseID != request.SourceDatabaseID || !existing.RestorePointInTime.Equal(request.PointInTime) {
			return Database{}, false, ErrConflict
		}
		if request.SourceDefinition != nil && !restoreMatchesSourceDefinition(existing, source) {
			return Database{}, false, ErrConflict
		}
		if existing.State == StateReady {
			return existing, false, nil
		}
		database, err := s.Reconcile(ctx, request.AccountID, existing.ID)
		return database, false, err
	}
	if !errors.Is(err, ErrNotFound) {
		return Database{}, false, err
	}
	if !request.PointInTime.Before(now) || currentRestoreWindow <= 0 || source.Spec.RestoreWindowSeconds <= 0 ||
		now.Sub(request.PointInTime) > time.Duration(currentRestoreWindow)*time.Second ||
		now.Sub(request.PointInTime) > time.Duration(source.Spec.RestoreWindowSeconds)*time.Second {
		return Database{}, false, ErrInvalid
	}
	reservationLimit, err := s.AdmitRestoreReservation(ctx, request.AccountID, RestoreSourceDefinition{
		Spec: source.Spec, BackendID: source.BackendID, BackendFingerprint: source.BackendFingerprint, ProviderResourceID: source.ProviderResourceID, DataResourceID: source.DataResourceID})
	if err != nil {
		return Database{}, false, err
	}
	database, created, err := s.store.Reserve(ctx, Database{
		ID:                      s.newID(),
		AccountID:               request.AccountID,
		Name:                    request.Name,
		Spec:                    source.Spec,
		BackendID:               source.BackendID,
		BackendFingerprint:      source.BackendFingerprint,
		RestoreSourceDatabaseID: source.ID,
		RestoreSourceResourceID: databaseDataResource(source),
		RestorePointInTime:      request.PointInTime.UTC(),
		State:                   StateProvisioning,
		DesiredGeneration:       1,
		CreatedAt:               now,
		UpdatedAt:               now,
	}, reservationLimit)
	if err != nil {
		return Database{}, false, err
	}
	if database.RestoreSourceDatabaseID != request.SourceDatabaseID || !database.RestorePointInTime.Equal(request.PointInTime.UTC()) {
		return Database{}, false, ErrConflict
	}
	if request.SourceDefinition != nil && !restoreMatchesSourceDefinition(database, source) {
		return Database{}, false, ErrConflict
	}
	if database.State == StateReady {
		return database, created, nil
	}
	ready, err := s.Reconcile(ctx, request.AccountID, database.ID)
	if err != nil {
		return database, created, err
	}
	return ready, created, nil
}

// AdmitRestoreReservation validates operator rollout, account admission,
// provider capabilities and entitlements without provider IO. Internal clone
// writers must separately authenticate their capture and atomically reserve
// the target with its owner and live source lineage. This grants no access to
// an existing private database.
func (s *Service) AdmitRestoreReservation(ctx context.Context, accountID string, definition RestoreSourceDefinition) (int, error) {
	if !s.provisioningEnabled() || !s.provisioningAllowed(ctx, accountID) {
		return 0, ErrUnavailable
	}
	if accountID == "" || definition.Spec.Validate() != nil || definition.Spec.RestoreWindowSeconds <= 0 || definition.ProviderResourceID == "" ||
		definition.DataResourceID != "" && !validDataResourceID(definition.DataResourceID) {
		return 0, ErrInvalid
	}
	if s.admit != nil {
		if err := s.admit(ctx, accountID); err != nil {
			return 0, err
		}
	}
	backend, err := s.registry.Resolve(definition.BackendID, definition.BackendFingerprint)
	if err != nil {
		return 0, err
	}
	if !backend.Capabilities.PointInTimeRestore || s.registry.UsagePolicy().Enabled && !backend.Capabilities.RestoreUsageIsolated && !backend.Capabilities.RestoreUsageIncludedInSource {
		return 0, ErrUnsupported
	}
	if err := backend.Capabilities.Supports(definition.Spec); err != nil {
		return 0, err
	}
	return s.reservationLimit(ctx, accountID)
}

func restoreMatchesSourceDefinition(target, source Database) bool {
	return target.ID != source.ID && target.Spec == source.Spec && target.BackendID == source.BackendID && target.BackendFingerprint == source.BackendFingerprint &&
		target.RestoreSourceResourceID == databaseDataResource(source) && target.State != StateDeleting && target.State != StateDeleted &&
		(target.ProviderResourceID == "" || target.ProviderResourceID != source.ProviderResourceID && target.ProviderResourceID != databaseDataResource(source))
}

func (s *Service) Reconcile(ctx context.Context, accountID, databaseID string) (Database, error) {
	database, err := s.store.Get(ctx, accountID, databaseID)
	if err != nil {
		return Database{}, err
	}
	switch database.State {
	case StateUpdating:
		return s.reconcileResize(ctx, database)
	case StateReady:
		if database.EnvironmentCloneOperationID != "" {
			proofs, ok := s.store.(CloneRestoreProofStore)
			if !ok {
				return Database{}, ErrUnsupported
			}
			proof, err := proofs.GetCloneRestoreProof(ctx, accountID, databaseID)
			if err != nil {
				return Database{}, err
			}
			if err := validateCloneRestoreProof(database, proof); err != nil {
				return Database{}, err
			}
		}
		return database, nil
	case StateDeleting, StateDeleted:
		return Database{}, ErrConflict
	case StateProvisioning, StateFailed:
		if !s.provisioningEnabled() || !s.provisioningAllowed(ctx, accountID) {
			return Database{}, ErrUnavailable
		}
	default:
		return Database{}, ErrConflict
	}
	if database.EnvironmentCloneOperationID != "" {
		if _, ok := s.store.(CloneRestoreProofStore); !ok {
			return Database{}, ErrUnsupported
		}
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
		if err := s.store.BeginAccounting(ctx, database.ID, leaseToken, s.now()); err != nil {
			return Database{}, s.releaseProviderError(ctx, database, StateProvisioning, err)
		}
		database.AccountingRequired = true
		if err := providerContext.Err(); err != nil {
			return Database{}, s.releaseProviderError(ctx, database, StateProvisioning, err)
		}
		if database.RestoreSourceResourceID != "" {
			observed, err = backend.Provider.Restore(providerContext, RestoreRequest{
				ResourceID:       database.ID,
				SourceResourceID: database.RestoreSourceResourceID,
				Spec:             database.Spec,
				PointInTime:      database.RestorePointInTime,
				IdempotencyKey:   "restore-" + database.ID,
			})
		} else {
			observed, err = backend.Provider.Provision(providerContext, ProvisionRequest{
				ResourceID:     database.ID,
				Spec:           database.Spec,
				IdempotencyKey: "provision-" + database.ID,
			})
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
	if err := validateCloneRestoreObservation(database, observed); err != nil {
		code := "restore_lineage_unavailable"
		if errors.Is(err, ErrConflict) {
			code = "restore_lineage_mismatch"
		}
		return Database{}, s.releaseKnownError(ctx, database, StateProvisioning, code, err, retryDelay(err, database.AttemptCount))
	}
	if database.ProviderResourceID == "" {
		if observed.ProviderResourceID == "" {
			return Database{}, s.releaseProviderError(ctx, database, StateProvisioning, ErrUnavailable)
		}
		if err := s.recordProviderResource(ctx, database.ID, leaseToken, observed.ProviderResourceID); err != nil {
			return Database{}, s.releaseProviderError(ctx, database, StateProvisioning, err)
		}
		database.ProviderResourceID = observed.ProviderResourceID
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
		if database.EnvironmentCloneOperationID != "" {
			finishContext, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), defaultStoreTimeout)
			defer finishCancel()
			result, finishErr := s.store.(CloneRestoreProofStore).FinishCloneRestoreProvision(finishContext, database, observed, s.now())
			if finishErr != nil {
				return Database{}, s.releaseProviderError(ctx, database, StateProvisioning, finishErr)
			}
			return result, nil
		}
		if observed.DataResourceID != "" {
			pins, ok := s.store.(DataResourceProvisionStore)
			if !ok {
				return Database{}, s.releaseKnownError(ctx, database, StateProvisioning, "data_identity_unavailable", ErrUnsupported, time.Hour)
			}
			finishContext, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), defaultStoreTimeout)
			defer finishCancel()
			result, finishErr := pins.FinishProvisionWithDataResource(finishContext, database, observed, s.now())
			if finishErr != nil {
				return Database{}, s.releaseProviderError(ctx, database, StateProvisioning, finishErr)
			}
			return result, nil
		}
		return s.finishProvision(ctx, database.ID, leaseToken)
	case ProviderStatusFailed:
		return Database{}, s.releaseKnownError(ctx, database, StateFailed, "provider_failed", ErrUnavailable, time.Hour)
	default:
		return Database{}, s.releaseProviderError(ctx, database, StateFailed, ErrUnavailable)
	}
}

// Clone-owned reservations must prove their physical origin before adopting a
// provider identity, and again when asynchronous provisioning becomes ready.
// The publication gate separately requires durable, coordinated data evidence.
func validateCloneRestoreObservation(database Database, observed ObservedDatabase) error {
	if database.EnvironmentCloneOperationID == "" {
		return nil
	}
	if observed.RestoreLineage == nil || observed.RestoreLineage.SourceResourceID == "" || observed.RestoreLineage.PointInTime.IsZero() || observed.ProviderResourceID == "" || !validDataResourceID(observed.DataResourceID) {
		return ErrUnavailable
	}
	if database.RestoreSourceDatabaseID == "" || database.RestoreSourceResourceID == "" || database.RestorePointInTime.IsZero() ||
		observed.ProviderResourceID == database.RestoreSourceResourceID || observed.DataResourceID == database.RestoreSourceResourceID || database.DataResourceID != "" && database.DataResourceID != observed.DataResourceID ||
		observed.RestoreLineage.SourceResourceID != database.RestoreSourceResourceID ||
		!observed.RestoreLineage.PointInTime.Equal(database.RestorePointInTime) {
		return ErrConflict
	}
	return nil
}

func (s *Service) Delete(ctx context.Context, accountID, databaseID string) (Database, error) {
	database, err := s.Get(ctx, accountID, databaseID)
	if err != nil {
		return Database{}, err
	}
	if database.State == StateDeleted {
		return database, nil
	}
	now := s.now()
	leaseToken := s.newLeaseToken()
	database, err = s.store.ClaimDelete(ctx, accountID, databaseID, leaseToken, now, now.Add(s.leaseDuration))
	if err != nil {
		return Database{}, err
	}
	if database.ProviderResourceID == "" && !database.AccountingRequired {
		// New reservations that never reached provider I/O have nothing to
		// destroy. Legacy reservations are conservatively backfilled instead.
		return s.finishDelete(ctx, database.ID, leaseToken)
	}
	backend, err := s.registry.Resolve(database.BackendID, database.BackendFingerprint)
	if err != nil {
		return Database{}, s.releaseKnownError(ctx, database, StateDeleting, "backend_unavailable", ErrUnavailable, time.Hour)
	}
	providerContext, cancel := context.WithTimeout(ctx, s.providerTimeout)
	defer cancel()
	if database.ProviderResourceID == "" {
		identity, discoverErr := discoverResource(providerContext, backend.Provider, database)
		if errors.Is(discoverErr, ErrNotFound) {
			// A timed-out creation may still appear later. Absence now does
			// not prove either final shutdown or zero historical consumption.
			discoverErr = ErrUnavailable
		}
		if discoverErr != nil {
			return Database{}, s.releaseProviderError(ctx, database, StateDeleting, discoverErr)
		}
		if err := s.recordProviderResource(ctx, database.ID, leaseToken, identity); err != nil {
			return Database{}, s.releaseProviderError(ctx, database, StateDeleting, err)
		}
		database.ProviderResourceID = identity
	}
	if err := providerContext.Err(); err != nil {
		return Database{}, s.releaseProviderError(ctx, database, StateDeleting, err)
	}
	result, err := backend.Provider.Delete(providerContext, DeleteRequest{
		ResourceID:              database.ID,
		ProviderResourceID:      database.ProviderResourceID,
		RestoreSourceResourceID: database.RestoreSourceResourceID,
		RestorePointInTime:      database.RestorePointInTime,
		IdempotencyKey:          "delete-" + database.ID,
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
	database, err := customerDatabase(ctx, s.store, accountID, databaseID)
	if err != nil {
		return Database{}, err
	}
	rows, err := s.withHealth(ctx, accountID, []Database{database})
	if err != nil {
		return Database{}, err
	}
	return rows[0], nil
}

// FindByName locates an account-owned durable reservation. Clone workers use
// their operation-specific name to recover after a crash before checkpointing
// the target ID, including after a completed restore's PITR window has expired.
func (s *Service) FindByName(ctx context.Context, accountID, name string) (Database, error) {
	return s.store.FindByName(ctx, accountID, name)
}

func (s *Service) List(ctx context.Context, accountID string) ([]Database, error) {
	if accountID == "" {
		return nil, ErrInvalid
	}
	var items []Database
	var err error
	if customers, ok := s.store.(CustomerDatabaseStore); ok {
		items, err = customers.ListCustomerDatabases(ctx, accountID)
	} else {
		items, err = s.store.List(ctx, accountID)
	}
	if err != nil {
		return nil, err
	}
	visible := make([]Database, 0, len(items))
	for _, database := range items {
		if database.EnvironmentCloneOperationID == "" {
			visible = append(visible, database)
		}
	}
	return s.withHealth(ctx, accountID, visible)
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
