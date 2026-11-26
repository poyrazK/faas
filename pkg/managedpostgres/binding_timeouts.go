package managedpostgres

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/dbcreds"
)

// BindingTimeoutDefaults are PostgreSQL role defaults expressed in milliseconds.
type BindingTimeoutDefaults struct {
	StatementMS         int32 `json:"statement_timeout_ms"`
	LockMS              int32 `json:"lock_timeout_ms"`
	IdleInTransactionMS int32 `json:"idle_in_transaction_session_timeout_ms"`
}

// BindingTimeoutPatch changes only the supplied defaults. Zero disables a timeout.
type BindingTimeoutPatch struct {
	StatementMS         *int32 `json:"statement_timeout_ms,omitempty"`
	LockMS              *int32 `json:"lock_timeout_ms,omitempty"`
	IdleInTransactionMS *int32 `json:"idle_in_transaction_session_timeout_ms,omitempty"`
}

func (p BindingTimeoutPatch) apply(defaults BindingTimeoutDefaults) BindingTimeoutDefaults {
	if p.StatementMS != nil {
		defaults.StatementMS = *p.StatementMS
	}
	if p.LockMS != nil {
		defaults.LockMS = *p.LockMS
	}
	if p.IdleInTransactionMS != nil {
		defaults.IdleInTransactionMS = *p.IdleInTransactionMS
	}
	return defaults
}

func (p BindingTimeoutPatch) Validate() error {
	if p.StatementMS == nil && p.LockMS == nil && p.IdleInTransactionMS == nil {
		return ErrInvalid
	}
	return p.apply(BindingTimeoutDefaults{}).Validate()
}

func (d BindingTimeoutDefaults) Validate() error {
	if d.StatementMS < 0 || d.LockMS < 0 || d.IdleInTransactionMS < 0 {
		return ErrInvalid
	}
	return nil
}

type BindingTimeoutPolicy struct {
	BindingTimeoutDefaults
	Revision int64 `json:"revision"`
}

type BindingTimeoutStatus struct {
	BindingID string `json:"binding_id"`
	BindingTimeoutPolicy
	State     string     `json:"state"`
	AppliedAt *time.Time `json:"applied_at,omitempty"`
}

func (s *BindingTimeoutStatus) setState(now time.Time) {
	switch {
	case s.Revision == 0:
		s.State = "default"
	case s.AppliedAt == nil:
		s.State = "pending"
	case now.Sub(*s.AppliedAt) < dbcreds.PoolPolicyMaxAge:
		s.State = "refreshing"
	default:
		s.State = "applied"
	}
}

type BindingTimeoutProvider interface {
	ApplyBindingTimeouts(ctx context.Context, resourceID, role string, policy BindingTimeoutPolicy) error
}

type bindingTimeoutStore interface {
	bindingTimeouts(ctx context.Context, bindingID string) (BindingTimeoutStatus, error)
	saveBindingTimeouts(ctx context.Context, binding Binding, patch BindingTimeoutPatch) (BindingTimeoutStatus, error)
	dueBindingTimeouts(ctx context.Context, limit int) ([]Binding, error)
	applyBindingTimeouts(ctx context.Context, binding Binding, resourceID string, apply BindingTimeoutProvider) error
}

func bindingTimeoutAccess(access CredentialAccess) bool {
	return access == CredentialReadWrite || access == CredentialReadOnly
}

func (s *BindingService) bindingTimeoutBackend(ctx context.Context, binding Binding) (string, BindingTimeoutProvider, error) {
	if !bindingTimeoutAccess(binding.Access) {
		return "", nil, ErrUnsupported
	}
	database, err := customerDatabase(ctx, s.databases, binding.DatabaseNamespace(), binding.DatabaseID)
	if err != nil {
		return "", nil, err
	}
	backend, err := s.registry.Resolve(database.BackendID, database.BackendFingerprint)
	if err != nil {
		return "", nil, err
	}
	provider, ok := backend.Provider.(BindingTimeoutProvider)
	if !ok {
		return "", nil, ErrUnsupported
	}
	return databaseDataResource(database), provider, nil
}

func (s *BindingService) BindingTimeouts(ctx context.Context, accountID, id string) (BindingTimeoutStatus, error) {
	return s.bindingTimeoutSettings(ctx, accountID, id, nil)
}

func (s *BindingService) SetBindingTimeouts(ctx context.Context, accountID, id string, patch BindingTimeoutPatch) (BindingTimeoutStatus, error) {
	if err := patch.Validate(); err != nil {
		return BindingTimeoutStatus{}, err
	}
	return s.bindingTimeoutSettings(ctx, accountID, id, &patch)
}

func (s *BindingService) bindingTimeoutSettings(ctx context.Context, accountID, id string, patch *BindingTimeoutPatch) (BindingTimeoutStatus, error) {
	binding, err := s.bindings.GetBinding(ctx, accountID, id)
	if err != nil {
		return BindingTimeoutStatus{}, err
	}
	if _, _, err = s.bindingTimeoutBackend(ctx, binding); err != nil {
		return BindingTimeoutStatus{}, err
	}
	store, ok := s.bindings.(bindingTimeoutStore)
	if !ok {
		return BindingTimeoutStatus{}, ErrUnsupported
	}
	var status BindingTimeoutStatus
	if patch == nil {
		status, err = store.bindingTimeouts(ctx, id)
	} else {
		status, err = store.saveBindingTimeouts(ctx, binding, *patch)
	}
	status.setState(s.now())
	return status, err
}

func (s *BindingService) attachBindingTimeouts(ctx context.Context, provider Provider, request *CredentialRequest) error {
	if _, ok := provider.(BindingTimeoutProvider); !ok {
		return nil
	}
	store, ok := s.bindings.(bindingTimeoutStore)
	if !ok || !bindingTimeoutAccess(request.Access) {
		return nil
	}
	status, err := store.bindingTimeouts(ctx, request.BindingID)
	request.TimeoutPolicy = status.BindingTimeoutPolicy
	return err
}

func (s *BindingService) sweepBindingTimeouts(ctx context.Context, limit int) error {
	store, ok := s.bindings.(bindingTimeoutStore)
	if !ok {
		return nil
	}
	bindings, err := store.dueBindingTimeouts(ctx, limit)
	if err != nil {
		return err
	}
	var failures []error
	for _, binding := range bindings {
		resource, provider, err := s.bindingTimeoutBackend(ctx, binding)
		if err == nil {
			applyCtx, cancel := context.WithTimeout(ctx, s.providerTimeout)
			err = store.applyBindingTimeouts(applyCtx, binding, resource, provider)
			cancel()
		}
		if err != nil && !errors.Is(err, ErrConflict) && !errors.Is(err, ErrNotFound) {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
