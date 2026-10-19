package state

import (
	"bytes"
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

// InvocationDepthError reports that a capped producer found its app's
// pending work at Invocation.AdmissionMaxPending inside the admission
// transaction. It unwraps to ErrQuotaExceeded and carries the safe limit
// metadata the API problem needs.
type InvocationDepthError struct{ Limit, Observed int }

func (e *InvocationDepthError) Error() string { return ErrQuotaExceeded.Error() }
func (e *InvocationDepthError) Unwrap() error { return ErrQuotaExceeded }

// lockInvocationAdmissionAppDB serialises capped admissions for one app on
// its apps row, the lock queue canaries, object notifications and binding
// verification already take before counting the same backlog. It must run
// before any statement that inserts a row referencing apps: those take FOR
// KEY SHARE on the row, and two transactions upgrading it to FOR UPDATE
// would deadlock.
func lockInvocationAdmissionAppDB(ctx context.Context, tx pgx.Tx, inv Invocation) error {
	if inv.AdmissionMaxPending <= 0 {
		return nil
	}
	app, err := productionWorkUUID(inv.AppID)
	if err != nil {
		return err
	}
	account, err := productionWorkUUID(inv.AccountID)
	if err != nil {
		return err
	}
	if _, err := sqlc.New().LockQueueCanaryApp(ctx, tx, sqlc.LockQueueCanaryAppParams{AccountID: account, AppID: app}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

// checkInvocationAdmissionDepthDB counts the app's pending production work
// for the producer's source while the caller holds the app lock. Keyed
// callers run it after keep_latest supersedes the lane, so replaced work
// does not count against the cap.
func checkInvocationAdmissionDepthDB(ctx context.Context, tx pgx.Tx, inv Invocation) error {
	if inv.AdmissionMaxPending <= 0 {
		return nil
	}
	app, err := productionWorkUUID(inv.AppID)
	if err != nil {
		return err
	}
	n, err := sqlc.New().CountProductionPendingInvocations(ctx, tx, sqlc.CountProductionPendingInvocationsParams{AppID: app, Source: string(inv.Source)})
	if err != nil {
		return err
	}
	return invocationAdmissionDepth(inv, int(n))
}

func invocationAdmissionDepth(inv Invocation, observed int) error {
	if inv.AdmissionMaxPending > 0 && observed >= inv.AdmissionMaxPending {
		return &InvocationDepthError{Limit: inv.AdmissionMaxPending, Observed: observed}
	}
	return nil
}

// pendingProductionInvocationsLocked mirrors CountProductionPendingInvocations
// under m.mu. superseded excludes rows a keep_latest admission is about to
// replace, matching the Pg count taken after WorkAdmissionSupersede.
func (m *MemStore) pendingProductionInvocationsLocked(appID string, source InvocationSource, superseded func(Invocation) bool) int {
	n := 0
	for _, inv := range m.invocations {
		if inv.AppID != appID || inv.Source != source || !m.productionInvocationWorkLocked(inv) {
			continue
		}
		if inv.State != InvocationPending && inv.State != InvocationDispatching {
			continue
		}
		if superseded != nil && superseded(inv) {
			continue
		}
		n++
	}
	return n
}

// checkKeyedAdmissionDepthLocked applies the cap before a keyed admission
// mutates anything, so MemStore needs no rollback when it refuses.
func (m *MemStore) checkKeyedAdmissionDepthLocked(inv Invocation, policy workpolicy.Policy, digest []byte) error {
	if inv.AdmissionMaxPending <= 0 {
		return nil
	}
	var superseded func(Invocation) bool
	if policy.PendingUpdates == workpolicy.PendingKeepLatest {
		superseded = func(old Invocation) bool {
			return old.State == InvocationPending && old.WorkPolicyName == policy.Name && bytes.Equal(old.WorkKeyDigest, digest)
		}
	}
	return invocationAdmissionDepth(inv, m.pendingProductionInvocationsLocked(inv.AppID, inv.Source, superseded))
}
