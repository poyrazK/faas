package meter

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// SuspensionCommands delegates the billing/outbox transaction to apid. Meterd
// retains its scheduler/notifier follow-up and never receives decryption keys.
type SuspensionCommands interface {
	Suspend(context.Context, string, time.Time) (api.InternalMailSuspensionResponse, error)
	Complete(context.Context, string, string, string) error
}

func (d *Dunning) suspendWithNotice(ctx context.Context, acct state.Account) (bool, error) {
	if d.p.SuspensionRequested == nil || !d.p.SuspensionRequested() || orgDunningSubject(acct) {
		return false, nil
	}
	if d.p.SuspensionCommands == nil || acct.PastDueAt == nil {
		return true, errors.New("suspension command unavailable")
	}
	result, err := d.p.SuspensionCommands.Suspend(ctx, acct.ID, *acct.PastDueAt)
	if err != nil || !result.Followup {
		return true, err
	}
	if !d.p.SuspensionRequested() {
		return true, errors.New("suspension command disabled")
	}
	d.suspensionFollowup(ctx, acct, result.SuspendedAt)
	return true, d.p.SuspensionCommands.Complete(ctx, acct.ID, result.IntentID, result.LeaseID)
}

// orgDunningSubject reports whether a dunning row is a company organization's
// billing subject. apid's suspension and non-payment deletion commands lock,
// transition and mail account rows only, so an organization never left
// past_due or suspended there (Hunt 17 H17-3). Organizations take the direct
// MarkDunningStep transition, which resolves organization subjects.
func orgDunningSubject(acct state.Account) bool {
	return acct.ResourceOrgID != ""
}

func (d *Dunning) suspensionFollowup(ctx context.Context, acct state.Account, at time.Time) {
	notifyAccountAppLifecycle(ctx, d.p.Store, d.p.Notif, acct.ID, "account_suspended", d.p.Log)
	d.parkAll(ctx, acct.ID)
	payload, _ := json.Marshal(dunningPayload{AccountID: acct.ID, At: at.UTC().Format(time.RFC3339Nano)})
	if err := d.p.Notif.Notify(ctx, db.NotifyBillingPastDue, string(payload)); err != nil {
		d.p.Log.Warn("meter: dunning notify billing_past_due", "account", acct.ID, "err", err)
	}
}
