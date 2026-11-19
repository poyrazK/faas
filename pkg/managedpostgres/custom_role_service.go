package managedpostgres

import (
	"context"
	"errors"
	"slices"
	"time"
)

// CustomRoleService owns custom-role declarations (ADR-20261116213000): it
// validates and stores them, then applies the database's whole declaration
// through the provider. A failed apply leaves rows unsettled for the binding
// reconciler's sweep to retry.
type CustomRoleService struct {
	registry        *Registry
	databases       Store
	store           CustomRoleStore
	providerTimeout time.Duration
	now             func() time.Time
}

// CustomRoleServiceOptions configures a CustomRoleService.
type CustomRoleServiceOptions struct {
	ProviderTimeout time.Duration
	Now             func() time.Time
}

// SaveCustomRoleRequest declares or replaces one role.
type SaveCustomRoleRequest struct {
	AccountID  string
	DatabaseID string
	Name       string
	BypassRLS  bool
	GrantedTo  []CredentialAccess
	MemberOf   []string
	// DefaultFor lists the access modes whose logins start as this role.
	// nil keeps the role's current defaults that are still granted, so a
	// client that does not know the field never clears it.
	DefaultFor *[]CredentialAccess
}

// NewCustomRoleService needs a store that also persists declarations.
func NewCustomRoleService(registry *Registry, databases Store, options CustomRoleServiceOptions) (*CustomRoleService, error) {
	store, ok := databases.(CustomRoleStore)
	if registry == nil || !ok {
		return nil, ErrInvalid
	}
	if options.ProviderTimeout == 0 {
		options.ProviderTimeout = defaultProviderTimeout
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	return &CustomRoleService{registry: registry, databases: databases, store: store, providerTimeout: options.ProviderTimeout, now: options.Now}, nil
}

// List returns the database's declared roles.
func (s *CustomRoleService) List(ctx context.Context, accountID, databaseID string) ([]CustomRole, error) {
	if _, err := customerDatabase(ctx, s.databases, accountID, databaseID); err != nil {
		return nil, err
	}
	return s.store.ListCustomRoles(ctx, accountID, databaseID)
}

// Save declares or replaces a role and applies the declaration. A refusal
// returns an error. When the apply could not finish the role is returned
// pending with LastError set, and the reconciler retries it.
func (s *CustomRoleService) Save(ctx context.Context, request SaveCustomRoleRequest) (CustomRole, error) {
	if err := ValidateCustomRoleName(request.Name); err != nil {
		return CustomRole{}, err
	}
	grants, err := NormalizeCustomRoleGrants(request.GrantedTo)
	if err != nil {
		return CustomRole{}, err
	}
	parents, err := normalizeNames(request.MemberOf)
	if err != nil {
		return CustomRole{}, err
	}
	database, backend, err := s.backendFor(ctx, request.AccountID, request.DatabaseID)
	if err != nil {
		return CustomRole{}, err
	}
	if database.State != StateReady || database.ProviderResourceID == "" {
		return CustomRole{}, ErrConflict
	}
	defaults, err := s.defaultsFor(ctx, request, grants)
	if err != nil {
		return CustomRole{}, err
	}
	role, err := s.store.SaveCustomRole(ctx, CustomRole{
		AccountID: request.AccountID, DatabaseID: request.DatabaseID, Name: request.Name,
		BypassRLS: request.BypassRLS, GrantedTo: grants, MemberOf: parents, DefaultFor: defaults,
	}, CustomRolesMax, s.now())
	if err != nil {
		return CustomRole{}, err
	}
	if err := s.apply(ctx, database, backend); err != nil {
		return role, err
	}
	return s.find(ctx, request.AccountID, request.DatabaseID, request.Name, role)
}

// defaultsFor resolves a request's DefaultFor: given, it is validated
// against grants; absent, the role's current defaults that grants still
// cover are kept.
func (s *CustomRoleService) defaultsFor(ctx context.Context, request SaveCustomRoleRequest, grants []CredentialAccess) ([]CredentialAccess, error) {
	if request.DefaultFor != nil {
		return NormalizeCustomRoleDefaults(*request.DefaultFor, grants)
	}
	roles, err := s.store.ListCustomRoles(ctx, request.AccountID, request.DatabaseID)
	if err != nil {
		return nil, err
	}
	var kept []CredentialAccess
	for _, role := range roles {
		if role.Name != request.Name || role.State == CustomRoleDeleting {
			continue
		}
		for _, access := range role.DefaultFor {
			if slices.Contains(grants, access) {
				kept = append(kept, access)
			}
		}
	}
	return NormalizeCustomRoleDefaults(kept, grants)
}

// Delete removes a role from the declaration and drops it from the database.
func (s *CustomRoleService) Delete(ctx context.Context, accountID, databaseID, name string) (CustomRole, error) {
	if err := ValidateCustomRoleName(name); err != nil {
		return CustomRole{}, err
	}
	database, backend, err := s.backendFor(ctx, accountID, databaseID)
	if err != nil {
		return CustomRole{}, err
	}
	role, err := s.store.DeleteCustomRole(ctx, accountID, databaseID, name, s.now())
	if err != nil {
		return CustomRole{}, err
	}
	if database.State != StateReady || database.ProviderResourceID == "" {
		return role, nil
	}
	if err := s.apply(ctx, database, backend); err != nil {
		return role, err
	}
	return s.find(ctx, accountID, databaseID, name, role)
}

// Reconcile applies one database's declaration again.
func (s *CustomRoleService) Reconcile(ctx context.Context, accountID, databaseID string) error {
	database, backend, err := s.backendFor(ctx, accountID, databaseID)
	if err != nil {
		return err
	}
	if database.State != StateReady || database.ProviderResourceID == "" {
		return s.store.FailCustomRoleApply(ctx, accountID, databaseID, "database_not_ready", s.now().Add(time.Minute), s.now())
	}
	return s.apply(ctx, database, backend)
}

// Sweep reconciles the databases with unsettled declarations.
func (s *CustomRoleService) Sweep(ctx context.Context, limit int) error {
	due, err := s.store.DueCustomRoleDatabases(ctx, limit, s.now())
	if err != nil {
		return err
	}
	var sweepErrors []error
	for _, item := range due {
		if err := s.Reconcile(ctx, item.AccountID, item.DatabaseID); err != nil && !errors.Is(err, ErrNotFound) {
			sweepErrors = append(sweepErrors, err)
		}
	}
	return errors.Join(sweepErrors...)
}

func (s *CustomRoleService) backendFor(ctx context.Context, accountID, databaseID string) (Database, Backend, error) {
	database, err := customerDatabase(ctx, s.databases, accountID, databaseID)
	if err != nil {
		return Database{}, Backend{}, err
	}
	backend, err := s.registry.Resolve(database.BackendID, database.BackendFingerprint)
	if err != nil {
		return Database{}, Backend{}, err
	}
	if _, ok := backend.Provider.(CustomRoleApplier); !ok {
		return Database{}, Backend{}, ErrCustomRolesUnsupported
	}
	return database, backend, nil
}

func (s *CustomRoleService) find(ctx context.Context, accountID, databaseID, name string, fallback CustomRole) (CustomRole, error) {
	roles, err := s.store.ListCustomRoles(ctx, accountID, databaseID)
	if err != nil {
		return fallback, err
	}
	for _, role := range roles {
		if role.Name == name {
			return role, nil
		}
	}
	// A settled delete removes the row.
	fallback.State, fallback.LastError = CustomRoleDeleted, ""
	return fallback, nil
}

// apply sends the whole declaration to the provider and settles the
// revisions it read.
func (s *CustomRoleService) apply(ctx context.Context, database Database, backend Backend) error {
	applier, ok := backend.Provider.(CustomRoleApplier)
	if !ok {
		return ErrCustomRolesUnsupported
	}
	roles, err := s.store.ListCustomRoles(ctx, database.AccountID, database.ID)
	if err != nil {
		return err
	}
	applied := make(map[string]int64, len(roles))
	attempt := 0
	for _, role := range roles {
		applied[role.Name] = role.Revision
		attempt = max(attempt, role.AttemptCount)
	}
	callCtx, cancel := context.WithTimeout(ctx, s.providerTimeout)
	applyErr := applier.ApplyCustomRoles(callCtx, databaseDataResource(database), DesiredCustomRoles(roles))
	cancel()
	now := s.now()
	if applyErr != nil {
		code := providerErrorCode(applyErr)
		switch {
		case errors.Is(applyErr, ErrBypassRLSConflict):
			code = "bypass_rls_conflict"
		case errors.Is(applyErr, ErrBypassRLSUnavailable):
			code = "bypass_rls_unavailable"
		}
		retryAt := now.Add(retryDelay(normalizeProviderError(applyErr), int32(attempt+1)))
		if err := s.store.FailCustomRoleApply(context.WithoutCancel(ctx), database.AccountID, database.ID, code, retryAt, now); err != nil {
			return errors.Join(applyErr, err)
		}
		if errors.Is(applyErr, ErrBypassRLSConflict) || errors.Is(applyErr, ErrBypassRLSUnavailable) {
			return applyErr
		}
		// Anything else is retried by the reconciler; the rows say why.
		return nil
	}
	return s.store.FinishCustomRoleApply(context.WithoutCancel(ctx), database.AccountID, database.ID, applied, now)
}

// customRoleGrants names the declared roles a login of access on database
// receives when issued, and the one it starts as ("" for none). Stores
// without declarations grant none.
func customRoleGrants(ctx context.Context, databases Store, database Database, access CredentialAccess) (CustomRoleGrants, string, error) {
	store, ok := databases.(CustomRoleStore)
	if !ok {
		return "", "", nil
	}
	roles, err := store.ListCustomRoles(ctx, database.AccountID, database.ID)
	if errors.Is(err, ErrNotFound) {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	return CustomRoleGrantsFor(roles, access), CustomRoleDefaultFor(roles, access), nil
}

// CustomRoles returns a custom-role service over the binding service's
// registry and database store (ADR-20261116213000).
func (s *BindingService) CustomRoles() (*CustomRoleService, error) {
	if s == nil {
		return nil, ErrUnavailable
	}
	return NewCustomRoleService(s.registry, s.databases, CustomRoleServiceOptions{ProviderTimeout: s.providerTimeout})
}
