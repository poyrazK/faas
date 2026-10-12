// memstore_rebalance_helpers.go — Tier A4 (ADR-064) test
// seams on *MemStore. These methods are exposed on the
// concrete *MemStore type (NOT on the Store interface) so
// the pkg/sched rebalance engine tests can craft fixtures
// that drive the ReassignAppOwner cooldown branch
// (SetAppReassignedAtForTest) and observe the per-app
// outcome after a RebalanceOrphanedApps call
// (AllAppsForTest).
//
// Naming convention: every method suffixed "ForTest" is
// unguarded, package-private in spirit (only MemStore
// implementations have them) and reserved for tests.
// Production code MUST NOT call these — a grep-for-"ForTest"
// is the load-bearing gate, mirroring the existing
// `reassignAppOwnerForTest` seam in
// memstore_reassign_test.go (PR #509 follow-up).
//
// Why a non-test file? pkg/sched rebalance_engine_test.go
// is in `package sched`; test-only methods in
// `package state`'s `_test.go` files are not visible across
// packages. Putting the helpers in this production file
// (with the ForTest suffix + a tight docs contract) is the
// smallest-blast-radius way to expose them.

package state

import (
	"context"
	"time"
)

// AllAppsForTest returns every app row currently in the
// store, regardless of status or owner. The pkg/sched
// rebalance engine tests assert per-app state after a
// RebalanceOrphanedApps call; the public Store surface
// deliberately doesn't expose a "list all apps"
// iterator.
//
// NOT goroutine-safe with concurrent writes — the engine
// tests are single-threaded by construction.
func (m *MemStore) AllAppsForTest() []App {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]App, 0, len(m.apps))
	for _, a := range m.apps {
		out = append(out, a)
	}
	return out
}

// SetAppReassignedAtForTest stamps apps.reassigned_at on
// the given app row. Bypasses the cooldown gate in
// ReassignAppOwner (which would refuse to stamp an
// already-recently-stamped row) so the cooldown filter test
// can craft "stamped 90s ago" fixtures without circular
// logic.
//
// Returns ErrNotFound when the appID is missing.
func (m *MemStore) SetAppReassignedAtForTest(_ context.Context, appID string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.apps[appID]
	if !ok {
		return ErrNotFound
	}
	a.ReassignedAt = &at
	m.putAppLocked(appID, a)
	return nil
}
