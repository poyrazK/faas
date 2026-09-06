package managedpostgres

import (
	"context"
	"sort"
	"time"
)

var _ BindingStore = (*MemoryStore)(nil)

func (s *MemoryStore) ReserveBinding(_ context.Context, binding Binding) (Binding, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateBindingReservation(binding); err != nil {
		return Binding{}, false, err
	}
	database, exists := s.databases[binding.DatabaseID]
	if !exists || database.AccountID != binding.AccountID {
		return Binding{}, false, ErrNotFound
	}
	if database.State != StateReady {
		return Binding{}, false, ErrConflict
	}
	if existing, ok := s.bindings[binding.ID]; ok {
		if !sameBindingReservation(existing, binding) {
			return Binding{}, false, ErrConflict
		}
		return cloneBinding(existing), false, nil
	}
	target := bindingTarget(binding.AppID, binding.Scope, binding.EnvironmentKey)
	if id, ok := s.targets[target]; ok {
		existing := s.bindings[id]
		if existing.DatabaseID != binding.DatabaseID || existing.Access != binding.Access {
			return Binding{}, false, ErrConflict
		}
		return cloneBinding(existing), false, nil
	}
	if binding.RetryAt.IsZero() {
		binding.RetryAt = binding.CreatedAt
	}
	s.bindings[binding.ID] = cloneBinding(binding)
	s.targets[target] = binding.ID
	return cloneBinding(binding), true, nil
}

func (s *MemoryStore) GetBinding(_ context.Context, accountID, bindingID string) (Binding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	binding, ok := s.bindings[bindingID]
	if !ok || binding.AccountID != accountID {
		return Binding{}, ErrNotFound
	}
	return cloneBinding(binding), nil
}

func (s *MemoryStore) ListBindings(_ context.Context, accountID, databaseID string) ([]Binding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]Binding, 0)
	for _, binding := range s.bindings {
		if binding.AccountID == accountID && binding.DatabaseID == databaseID && binding.State != BindingStateDeleted {
			items = append(items, cloneBinding(binding))
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	return items, nil
}

func (s *MemoryStore) DueBindings(_ context.Context, includeProvisioning bool, limit int, now time.Time) ([]Binding, error) {
	if limit < 1 || limit > 100 || now.IsZero() {
		return nil, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]Binding, 0)
	for _, binding := range s.bindings {
		provisioning := binding.State == BindingStateProvisioning || binding.State == BindingStateFailed
		if binding.State != BindingStateDeleting && (!includeProvisioning || !provisioning) {
			continue
		}
		if binding.RetryAt.After(now) || binding.LeaseUntil.After(now) {
			continue
		}
		items = append(items, cloneBinding(binding))
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].RetryAt.Equal(items[j].RetryAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].RetryAt.Before(items[j].RetryAt)
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (s *MemoryStore) ClaimBinding(_ context.Context, accountID, bindingID, leaseToken string, operation BindingState, now, leaseUntil time.Time) (Binding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	binding, ok := s.bindings[bindingID]
	if !ok || binding.AccountID != accountID {
		return Binding{}, ErrNotFound
	}
	if leaseToken == "" || now.IsZero() || !leaseUntil.After(now) {
		return Binding{}, ErrInvalid
	}
	if !binding.LeaseUntil.IsZero() && binding.LeaseUntil.After(now) {
		return Binding{}, ErrConflict
	}
	switch operation {
	case BindingStateProvisioning:
		if (binding.State != BindingStateProvisioning && binding.State != BindingStateFailed) || binding.RetryAt.After(now) {
			return Binding{}, ErrConflict
		}
	case BindingStateDeleting:
		if binding.State == BindingStateDeleted {
			return Binding{}, ErrConflict
		}
	default:
		return Binding{}, ErrInvalid
	}
	if operation == BindingStateDeleting && binding.State != BindingStateDeleting {
		binding.AttemptCount = 0
	}
	if operation != binding.State {
		binding.LastErrorCode = ""
	}
	if binding.AttemptCount < 30 {
		binding.AttemptCount++
	}
	binding.State = operation
	binding.LeaseToken = leaseToken
	binding.LeaseUntil = leaseUntil
	binding.RetryAt = now
	binding.UpdatedAt = now
	s.bindings[bindingID] = binding
	return cloneBinding(binding), nil
}

func (s *MemoryStore) FinishBindingProvision(_ context.Context, bindingID, leaseToken, providerIdentityID, credentialRef string, now time.Time) (Binding, error) {
	if leaseToken == "" || !validOpaqueID(providerIdentityID) || !validOpaqueID(credentialRef) || now.IsZero() {
		return Binding{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	binding, ok := s.bindings[bindingID]
	if !ok {
		return Binding{}, ErrNotFound
	}
	if binding.State != BindingStateProvisioning || binding.LeaseToken != leaseToken || !binding.LeaseUntil.After(now) {
		return Binding{}, ErrConflict
	}
	binding.ProviderIdentityID = providerIdentityID
	binding.CredentialRef = credentialRef
	binding.State = BindingStateReady
	binding.LastErrorCode = ""
	binding.LeaseToken = ""
	binding.LeaseUntil = time.Time{}
	binding.AttemptCount = 0
	binding.RetryAt = now
	binding.UpdatedAt = now
	s.bindings[bindingID] = binding
	return cloneBinding(binding), nil
}

func (s *MemoryStore) ReleaseBinding(_ context.Context, bindingID, leaseToken string, next BindingState, errorCode string, now, retryAt time.Time) error {
	if leaseToken == "" || now.IsZero() || retryAt.Before(now) || !validErrorCode(errorCode) ||
		(next != BindingStateProvisioning && next != BindingStateDeleting && next != BindingStateFailed) {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	binding, ok := s.bindings[bindingID]
	if !ok {
		return ErrNotFound
	}
	if binding.LeaseToken != leaseToken || !binding.LeaseUntil.After(now) {
		return ErrConflict
	}
	binding.State = next
	binding.LastErrorCode = errorCode
	binding.LeaseToken = ""
	binding.LeaseUntil = time.Time{}
	binding.RetryAt = retryAt
	binding.UpdatedAt = now
	s.bindings[bindingID] = binding
	return nil
}

func (s *MemoryStore) FinishBindingDelete(_ context.Context, bindingID, leaseToken string, now time.Time) (Binding, error) {
	if leaseToken == "" || now.IsZero() {
		return Binding{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	binding, ok := s.bindings[bindingID]
	if !ok {
		return Binding{}, ErrNotFound
	}
	if binding.State != BindingStateDeleting || binding.LeaseToken != leaseToken || !binding.LeaseUntil.After(now) {
		return Binding{}, ErrConflict
	}
	binding.State = BindingStateDeleted
	binding.LastErrorCode = ""
	binding.LeaseToken = ""
	binding.LeaseUntil = time.Time{}
	binding.AttemptCount = 0
	binding.RetryAt = now
	binding.UpdatedAt = now
	binding.DeletedAt = &now
	delete(s.targets, bindingTarget(binding.AppID, binding.Scope, binding.EnvironmentKey))
	s.bindings[bindingID] = binding
	return cloneBinding(binding), nil
}

func validateBindingReservation(binding Binding) error {
	if binding.ID == "" || binding.AccountID == "" || binding.DatabaseID == "" || binding.AppID == "" ||
		!validBindingScope(binding.Scope) || !validEnvironmentKey(binding.EnvironmentKey) ||
		(binding.Access != CredentialReadWrite && binding.Access != CredentialReadOnly) ||
		binding.CredentialGeneration != 1 || binding.State != BindingStateProvisioning ||
		binding.ProviderIdentityID != "" || binding.CredentialRef != "" || binding.LastErrorCode != "" ||
		binding.LeaseToken != "" || !binding.LeaseUntil.IsZero() || binding.AttemptCount != 0 ||
		binding.CreatedAt.IsZero() || binding.UpdatedAt.IsZero() || binding.DeletedAt != nil {
		return ErrInvalid
	}
	return nil
}

func bindingTarget(appID, scope, environmentKey string) string {
	return appID + "\x00" + scope + "\x00" + environmentKey
}

func sameBindingReservation(left, right Binding) bool {
	return left.AccountID == right.AccountID && left.DatabaseID == right.DatabaseID &&
		left.AppID == right.AppID && left.Scope == right.Scope &&
		left.EnvironmentKey == right.EnvironmentKey && left.Access == right.Access
}

func cloneBinding(binding Binding) Binding {
	if binding.DeletedAt != nil {
		deletedAt := *binding.DeletedAt
		binding.DeletedAt = &deletedAt
	}
	return binding
}
