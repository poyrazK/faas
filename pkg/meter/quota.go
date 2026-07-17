package meter

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// ScheddParker is the slice of the scheddgrpc surface the quota loop needs
// when a Free account crosses 100 %: park every live instance so the box
// stops burning RAM on a suspended customer. Defining the interface here
// keeps meterd from importing pkg/scheddgrpc (which would invert the
// caller direction; ADR-019 records the meterd→schedd dependency).
type ScheddParker interface {
	ParkInstance(ctx context.Context, instanceID, reason string) error
}

// Notifier is the db.Notify surface the meter uses. Lives behind an
// interface so tests substitute a recorder.
type Notifier interface {
	Notify(ctx context.Context, channel, payload string) error
}

// QuotaAction is what EnforceQuota decided. Logged + returned; the caller
// (the meterd loop) fans out the side effects.
type QuotaAction struct {
	AccountID string
	Plan      api.Plan
	Action    string // "" | "warn" | "stop"
	UsedGB    float64
	QuotaGB   int
}

// EnforceQuota is the per-tick decision the meterd quota loop runs once
// per account. It is split from the loop so the test surface can pin the
// exact decision without standing up timers + schedd fakes.
//
// Behavior (spec §4.7, ADR-010, financial-model §1):
//
//   - Free + ≥100 %:    status → suspended; park every live instance; emit
//     `billing_past_due` pg_notify so the dashboard sees
//     it. Customer can still query usage; wake returns
//     402 (apid auth gate, handlers_ext.go).
//   - Free + <100 %:    if the account is still in `suspended`, restore
//     it to `active` (the customer paid/refreshed).
//   - Paid + ≥100 %:    overage accrues; emit `quota_warning` once per
//     UTC day (caller de-duplicates via last-warning
//     column on the account row).
//   - Paid + <100 %:    nothing.
//
// The Free restore step matters: pg_notify from Stripe's
// `invoice.payment_succeeded` updates accounts.status to `active` already
// (apid handler), so meterd's job on Free is to keep RAM off the box until
// the customer pays — i.e. suspend and stay suspended.
func EnforceQuota(
	ctx context.Context,
	store state.Store,
	notif Notifier,
	schedd ScheddParker,
	log *slog.Logger,
	account state.Account,
	usedGB float64,
	at time.Time,
) (QuotaAction, error) {
	res := CheckQuota(account.Plan, usedGB)
	act := QuotaAction{
		AccountID: account.ID, Plan: account.Plan,
		Action: res.Action, UsedGB: res.UsedGB, QuotaGB: res.QuotaGB,
	}
	switch res.Action {
	case "stop":
		// Free hard stop. Flip the account, fan out, park instances.
		if account.Status != state.AccountSuspended {
			if err := store.UpdateAccountStatus(ctx, account.ID, state.AccountSuspended); err != nil {
				return act, fmt.Errorf("meter: suspend %s: %w", account.ID, err)
			}
			log.Info("meter: free-tier hard stop", "account", account.ID, "used_gb", usedGB, "quota_gb", res.QuotaGB)
		}
		ins, err := store.ListInstancesForAccount(ctx, account.ID)
		if err != nil {
			return act, fmt.Errorf("meter: list instances: %w", err)
		}
		for _, in := range ins {
			if !state.State(in.State).CountsForRAM() {
				continue
			}
			if err := schedd.ParkInstance(ctx, in.ID, "quota_exceeded_free"); err != nil {
				log.Warn("meter: park instance failed", "instance", in.ID, "err", err)
				continue
			}
		}
		payload, _ := json.Marshal(map[string]any{
			"account_id": account.ID, "used_gb": usedGB,
			"quota_gb": res.QuotaGB, "at": at.UTC().Format(time.RFC3339Nano),
		})
		if err := notif.Notify(ctx, db.NotifyBillingPastDue, string(payload)); err != nil {
			log.Warn("meter: notify billing_past_due", "err", err)
		}
	case "warn":
		// Paid overage — one warning event. Caller (loop) keeps a
		// per-account last-warning date to avoid spamming dashboards.
		payload, _ := json.Marshal(map[string]any{
			"account_id": account.ID, "plan": string(account.Plan),
			"used_gb": usedGB, "quota_gb": res.QuotaGB,
			"at": at.UTC().Format(time.RFC3339Nano),
		})
		if err := notif.Notify(ctx, db.NotifyQuotaWarning, string(payload)); err != nil {
			log.Warn("meter: notify quota_warning", "err", err)
		}
		log.Info("meter: paid-tier quota warning", "account", account.ID, "used_gb", usedGB, "quota_gb", res.QuotaGB)
	}
	return act, nil
}
