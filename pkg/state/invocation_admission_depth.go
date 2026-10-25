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

// InvocationBacklogBytesError reports that a queue producer found its
// account's pending queue payload bytes at Invocation.AdmissionMaxBacklogBytes
// (ADR-20261022105723). Like InvocationDepthError it unwraps to
// ErrQuotaExceeded.
type InvocationBacklogBytesError struct{ Limit, Observed int64 }

func (e *InvocationBacklogBytesError) Error() string { return ErrQuotaExceeded.Error() }
func (e *InvocationBacklogBytesError) Unwrap() error { return ErrQuotaExceeded }

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

// checkInvocationAdmissionDepthDB applies the producer's caps while the
// caller holds the app lock. Keyed callers run it after keep_latest
// supersedes the lane, so replaced work does not count against the caps.
//
// The backlog counters (ADR-20261022105723) cover every pending row of the
// app and source, so a sum below the cap admits without counting; only a
// sum at the cap counts the app's pending production work, which excludes
// environment-owned rows.
func checkInvocationAdmissionDepthDB(ctx context.Context, tx pgx.Tx, inv Invocation) error {
	q := sqlc.New()
	if inv.AdmissionMaxBacklogBytes > 0 && inv.Source == InvocationQueue {
		account, err := productionWorkUUID(inv.AccountID)
		if err != nil {
			return err
		}
		held, err := q.SumAccountQueueBacklogBytes(ctx, tx, account)
		if err != nil {
			return err
		}
		if err := invocationAdmissionBytes(inv, held); err != nil {
			return err
		}
	}
	if inv.AdmissionMaxPending <= 0 {
		return nil
	}
	app, err := productionWorkUUID(inv.AppID)
	if err != nil {
		return err
	}
	upper, err := q.SumInvocationBacklogPending(ctx, tx, sqlc.SumInvocationBacklogPendingParams{AppID: app, Source: string(inv.Source)})
	if err != nil {
		return err
	}
	if upper < int64(inv.AdmissionMaxPending) {
		return nil
	}
	n, err := q.CountProductionPendingInvocations(ctx, tx, sqlc.CountProductionPendingInvocationsParams{AppID: app, Source: string(inv.Source)})
	if err != nil {
		return err
	}
	return invocationAdmissionDepth(inv, int(n))
}

// invocationAdmissionBytes refuses a queue message while the account's
// pending queue bytes are at the cap. A message admitted just under it may
// pass it by its own size.
func invocationAdmissionBytes(inv Invocation, held int64) error {
	if inv.AdmissionMaxBacklogBytes > 0 && inv.Source == InvocationQueue && held >= inv.AdmissionMaxBacklogBytes {
		return &InvocationBacklogBytesError{Limit: inv.AdmissionMaxBacklogBytes, Observed: held}
	}
	return nil
}

// queueBacklogBytesLocked mirrors SumAccountQueueBacklogBytes under m.mu:
// the payload bytes of the account's pending and dispatching queue rows.
// superseded excludes rows a keep_latest admission is about to replace.
func (m *MemStore) queueBacklogBytesLocked(accountID string, superseded func(Invocation) bool) int64 {
	var held int64
	for _, inv := range m.invocations {
		if inv.AccountID != accountID || inv.Source != InvocationQueue ||
			(inv.State != InvocationPending && inv.State != InvocationDispatching) {
			continue
		}
		if superseded != nil && superseded(inv) {
			continue
		}
		held += int64(len(inv.Payload))
	}
	return held
}

// checkInvocationAdmissionLocked mirrors checkInvocationAdmissionDepthDB.
func (m *MemStore) checkInvocationAdmissionLocked(inv Invocation, superseded func(Invocation) bool) error {
	if inv.AdmissionMaxBacklogBytes > 0 && inv.Source == InvocationQueue {
		if err := invocationAdmissionBytes(inv, m.queueBacklogBytesLocked(inv.AccountID, superseded)); err != nil {
			return err
		}
	}
	if inv.AdmissionMaxPending <= 0 {
		return nil
	}
	return invocationAdmissionDepth(inv, m.pendingProductionInvocationsLocked(inv.AppID, inv.Source, superseded))
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
	if inv.AdmissionMaxPending <= 0 && inv.AdmissionMaxBacklogBytes <= 0 {
		return nil
	}
	var superseded func(Invocation) bool
	if policy.PendingUpdates == workpolicy.PendingKeepLatest {
		superseded = func(old Invocation) bool {
			return old.State == InvocationPending && old.WorkPolicyName == policy.Name && bytes.Equal(old.WorkKeyDigest, digest)
		}
	}
	return m.checkInvocationAdmissionLocked(inv, superseded)
}
