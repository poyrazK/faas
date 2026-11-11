package managedpostgres

import (
	"context"
	"slices"
	"strings"
	"time"
)

// BindingRotationRefreshStore owns the durable scheduler handoff for a ready
// rotated credential. Completion still requires the scheduler's exact wake ID.
type BindingRotationRefreshStore interface {
	EnsureBindingRotationRefresh(context.Context, string, string, time.Time) (Binding, error)
}

func bindingRotationNeedsRefresh(binding Binding) bool {
	return binding.State == BindingStateReady && binding.Access != CredentialMigration &&
		binding.RotationPreviousGeneration > 0 && binding.RotationWakeID != "" && !binding.RotationCleanupReady
}

func (s *BindingService) reconcileRotationRefresh(ctx context.Context, binding Binding) (Binding, error) {
	if !bindingRotationNeedsRefresh(binding) {
		return binding, nil
	}
	store, ok := s.bindings.(BindingRotationRefreshStore)
	if !ok {
		return Binding{}, ErrUnsupported
	}
	return store.EnsureBindingRotationRefresh(ctx, binding.AccountID, binding.ID, s.now())
}

// BindingRotationRefresh is the scheduler intent modeled by MemoryStore's
// outbox. Production persists the same app and wake IDs in notification_outbox.
type BindingRotationRefresh struct {
	BindingID, AccountID, AppID, WakeID string
}

func (s *MemoryStore) queueBindingRotationRefreshLocked(binding Binding) {
	if bindingRotationNeedsRefresh(binding) {
		s.rotationRefreshes[binding.ID] = BindingRotationRefresh{BindingID: binding.ID, AccountID: binding.AccountID, AppID: binding.AppID, WakeID: binding.RotationWakeID}
	}
}

func (s *MemoryStore) EnsureBindingRotationRefresh(_ context.Context, accountID, bindingID string, now time.Time) (Binding, error) {
	if now.IsZero() {
		return Binding{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	binding, ok := s.bindings[bindingID]
	if !ok || binding.AccountID != accountID {
		return Binding{}, ErrNotFound
	}
	s.queueBindingRotationRefreshLocked(binding)
	return cloneBinding(binding), nil
}

// PendingBindingRotationRefreshes returns MemoryStore's undelivered scheduler
// intents for local wiring. It does not acknowledge a runtime refresh.
func (s *MemoryStore) PendingBindingRotationRefreshes() []BindingRotationRefresh {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []BindingRotationRefresh
	for _, binding := range s.bindings {
		if event, ok := s.rotationRefreshes[binding.ID]; ok && event.WakeID == binding.RotationWakeID && bindingRotationNeedsRefresh(binding) {
			out = append(out, event)
		}
	}
	slices.SortFunc(out, func(a, b BindingRotationRefresh) int { return strings.Compare(a.BindingID, b.BindingID) })
	return out
}
