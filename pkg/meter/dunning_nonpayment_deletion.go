package meter

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
)

// NonpaymentDeletionCommands delegates the paired transition and mail intent to
// apid. Meterd owns only the existing lifecycle notification follow-up.
type NonpaymentDeletionCommands interface {
	Schedule(context.Context, string, time.Time) (api.InternalMailNonpaymentDeletionResponse, error)
	Complete(context.Context, string, string, string) error
}

func (d *Dunning) scheduleNonpaymentDeletion(ctx context.Context, acct state.Account) (bool, error) {
	if d.p.NonpaymentDeletionRequested == nil || !d.p.NonpaymentDeletionRequested() || orgDunningSubject(acct) {
		return false, nil
	}
	if d.p.NonpaymentDeletionCommands == nil || acct.PastDueAt == nil {
		return true, errors.New("non-payment deletion command unavailable")
	}
	result, err := d.p.NonpaymentDeletionCommands.Schedule(ctx, acct.ID, *acct.PastDueAt)
	if err != nil || !result.Followup {
		return true, err
	}
	if !d.p.NonpaymentDeletionRequested() {
		return true, errors.New("non-payment deletion command disabled")
	}
	d.nonpaymentDeletionFollowup(ctx, acct, result.DeletionRequestedAt)
	return true, d.p.NonpaymentDeletionCommands.Complete(ctx, acct.ID, result.IntentID, result.LeaseID)
}

func (d *Dunning) nonpaymentDeletionFollowup(ctx context.Context, acct state.Account, at time.Time) {
	notifyAccountAppLifecycle(ctx, d.p.Store, d.p.Notif, acct.ID, "account_deleted_pending", d.p.Log)
	payload, _ := json.Marshal(dunningPayload{AccountID: acct.ID, At: at.UTC().Format(time.RFC3339Nano)})
	if err := d.p.Notif.Notify(ctx, db.NotifyAccountDeletionPending, string(payload)); err != nil {
		d.p.Log.Warn("meter: dunning notify account_deletion_pending", "account", acct.ID, "err", err)
	}
}

// deletionBlockTally counts, per guard, the accounts one sweep could not move
// to deleted_pending. Every label starts at zero so a cleared block resets.
type deletionBlockTally map[string]int

const deletionBlockedUnknown = "unknown"

func newDeletionBlockTally() deletionBlockTally {
	return deletionBlockTally{api.NonpaymentDeletionBlockedManagedPostgres: 0, api.NonpaymentDeletionBlockedSharedAppOwnership: 0, deletionBlockedUnknown: 0}
}

func (t deletionBlockTally) publish(g *prometheus.GaugeVec) {
	for reason, n := range t {
		g.WithLabelValues(reason).Set(float64(n))
	}
}

// Since the billing review of 2026-11-13 (R9-1) the ladder never moves an
// account to deleted_pending for non-payment, so no deletion guard can refuse
// it; the blocked gauge stays published at zero for its existing alert.
