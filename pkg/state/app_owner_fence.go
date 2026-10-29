// app_owner_fence.go — an owner scheduler's admission and the pressure
// rebalancer's ownership move serialize on the app row (ADR-20261028200212).
//
// Per-app max_concurrency (§6.2-1) is enforced by the owner scheduler's
// ledger, so it holds only while one scheduler admits an app's instances at
// a time. A wake checks ownership with a plain read before it admits, and
// the rebalancer used to check for resident instances with a plain read
// before it moved the app: a wake that passed its check could still insert
// an instance after the move, outside the new owner's ledger (H9-101 class).
//
// The fence closes that window without a new round trip:
//
//   - An admission bound with WithAppOwnerFence re-reads apps.node_id with
//     FOR KEY SHARE in the same transaction as the insert and refuses with
//     ErrAppOwnerMoved when another node owns the app. The insert's
//     instances_app_id_fkey check already takes that lock, so ordinary app
//     updates (FOR NO KEY UPDATE) still do not wait on admissions.
//   - ReassignParkedAppOwner takes FOR UPDATE on the app row, which waits
//     for every in-flight admission to commit, then checks for resident
//     instances in a fresh statement and refuses with ErrAppResident.
package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ErrAppOwnerMoved refuses an admission whose scheduler no longer owns the
// app. The scheduler treats it as a wake for another node's app.
var ErrAppOwnerMoved = errors.New("app owner moved")

// ErrAppResident refuses moving an app that still has resident instances.
var ErrAppResident = fmt.Errorf("app has resident instances: %w", ErrConflict)

type appOwnerFenceKey struct{}

type appOwnerFence struct{ ownerNodeID, appID string }

// WithAppOwnerFence binds an admission to the scheduler that owns the app on
// ownerNodeID. Empty ownerNodeID (single-box) binds nothing.
func WithAppOwnerFence(ctx context.Context, ownerNodeID string) context.Context {
	if ownerNodeID == "" {
		return ctx
	}
	return context.WithValue(ctx, appOwnerFenceKey{}, appOwnerFence{ownerNodeID: ownerNodeID})
}

// fenceApp names the app ctx's fence checks, for the store paths that only
// learn the app from the instance they insert.
func fenceApp(ctx context.Context, appID string) context.Context {
	fence, ok := ctx.Value(appOwnerFenceKey{}).(appOwnerFence)
	if !ok || appID == "" {
		return ctx
	}
	fence.appID = appID
	return context.WithValue(ctx, appOwnerFenceKey{}, fence)
}

// fencedApp is the app and owner ctx's fence checks, or ok=false.
func fencedApp(ctx context.Context) (appID, ownerNodeID string, ok bool) {
	fence, ok := ctx.Value(appOwnerFenceKey{}).(appOwnerFence)
	if !ok || fence.appID == "" || fence.ownerNodeID == "" {
		return "", "", false
	}
	return fence.appID, fence.ownerNodeID, true
}

// appOwnerAdmits mirrors the scheduler's ownership rule: an app with no node
// is every scheduler's.
func appOwnerAdmits(currentOwner, ownerNodeID string) error {
	if currentOwner != "" && currentOwner != ownerNodeID {
		return fmt.Errorf("state: %w: owner=%s admitting=%s", ErrAppOwnerMoved, currentOwner, ownerNodeID)
	}
	return nil
}

// checkAppOwnerStatementsDB is the fence for a database without the
// admission functions.
func checkAppOwnerStatementsDB(ctx context.Context, tx pgx.Tx) error {
	appID, ownerNodeID, ok := fencedApp(ctx)
	if !ok {
		return nil
	}
	var currentOwner string
	err := tx.QueryRow(ctx, `select coalesce(node_id::text, '') from apps where id = $1 for key share`, appID).Scan(&currentOwner)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil // the insert's foreign key reports it
	case err != nil:
		return fmt.Errorf("state: app owner fence: %w", err)
	}
	return appOwnerAdmits(currentOwner, ownerNodeID)
}

func (m *MemStore) checkAppOwnerFenceLocked(ctx context.Context, appID, newState string) error {
	if !IsLive(newState) {
		return nil
	}
	_, ownerNodeID, ok := fencedApp(fenceApp(ctx, appID))
	if !ok {
		return nil
	}
	app, exists := m.apps[appID]
	if !exists {
		return nil
	}
	return appOwnerAdmits(app.NodeID, ownerNodeID)
}

// appResidentStates is every state State.CountsForRAM accepts: an instance
// in one holds a reservation in its owner's ledger.
var appResidentStates = []string{
	string(StateWaking), string(StateColdBooting), string(StateRunning), string(StateDraining),
	string(StateWarm), string(StateSnapshotting), string(StateMigrating), string(StateFrozen),
}

// ParkedAppOwnerReassigner moves an app's ownership only while none of its
// instances is resident, atomically with the admissions it races
// (ADR-20261028200212). The pressure rebalancer prefers it to
// ReassignAppOwner.
type ParkedAppOwnerReassigner interface {
	ReassignParkedAppOwner(ctx context.Context, appID, fromNodeID, toNodeID string) error
}

// ReassignParkedAppOwner is ReassignAppOwner for an app with no resident
// instance. ErrConflict as ReassignAppOwner; ErrAppResident when an instance
// is resident, including one an admission committed while this waited.
func (s *PgStore) ReassignParkedAppOwner(ctx context.Context, appID, fromNodeID, toNodeID string) error {
	if appID == "" || fromNodeID == "" || toNodeID == "" {
		return fmt.Errorf("state: reassign parked app owner: empty id (app=%q from=%q to=%q)", appID, fromNodeID, toNodeID)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("state: reassign parked app owner: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var owner, status string
	err = tx.QueryRow(ctx, `select coalesce(node_id::text, ''), status from apps where id = $1 for update`, appID).Scan(&owner, &status)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ErrConflict
	case err != nil:
		return fmt.Errorf("state: reassign parked app owner: lock app: %w", err)
	}
	if owner != fromNodeID || (status != string(AppActive) && status != string(AppEvictedCold)) {
		return ErrConflict
	}
	// A fresh statement after the lock: it sees every admission that held
	// the app row's key-share lock until it committed.
	var resident bool
	if err := tx.QueryRow(ctx, `select exists(select 1 from instances where app_id = $1 and state = any($2::text[]))`,
		appID, appResidentStates).Scan(&resident); err != nil {
		return fmt.Errorf("state: reassign parked app owner: resident instances: %w", err)
	}
	if resident {
		return ErrAppResident
	}
	if _, err := tx.Exec(ctx, `update apps set node_id = $2, reassigned_at = now() where id = $1`, appID, toNodeID); err != nil {
		return fmt.Errorf("state: reassign parked app owner: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("state: reassign parked app owner: commit: %w", mapErr(err))
	}
	return nil
}

// ReassignParkedAppOwner mirrors PgStore.ReassignParkedAppOwner under m.mu.
func (m *MemStore) ReassignParkedAppOwner(_ context.Context, appID, fromNodeID, toNodeID string) error {
	if appID == "" || fromNodeID == "" || toNodeID == "" {
		return fmt.Errorf("state: reassign parked app owner: empty id (app=%q from=%q to=%q)", appID, fromNodeID, toNodeID)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.reassignableAppLocked(appID, fromNodeID); err != nil {
		if errors.Is(err, ErrNotFound) {
			return ErrConflict // as PgStore: the app is gone
		}
		return err
	}
	for _, ins := range m.instances {
		if ins.AppID == appID && State(ins.State).CountsForRAM() {
			return ErrAppResident
		}
	}
	m.reassignAppOwnerLocked(appID, fromNodeID, toNodeID)
	return nil
}
