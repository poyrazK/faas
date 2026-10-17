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

type QuotaWarningCommands interface {
	Warn(context.Context, string, time.Time, api.Plan) (api.InternalMailQuotaWarningResponse, error)
	Complete(context.Context, string, string, string) error
}

func (p QuotaParams) warnWithNotice(ctx context.Context, account state.Account, at time.Time) (bool, error) {
	if p.WarningRequested == nil || !p.WarningRequested() {
		return false, nil
	}
	if p.WarningCommands == nil {
		return true, errors.New("quota warning command unavailable")
	}
	result, err := p.WarningCommands.Warn(ctx, account.ID, at.UTC().Truncate(24*time.Hour), account.Plan)
	if err != nil || !result.Followup {
		return true, err
	}
	if !p.WarningRequested() {
		return true, errors.New("quota warning command disabled")
	}
	payload, _ := json.Marshal(map[string]any{
		"account_id": account.ID, "plan": string(result.Plan),
		"used_gb": api.InternalMailQuotaWarningUsageGBHours(result.UsedMBSeconds), "quota_gb": result.QuotaGB,
		"at": result.ObservedAt.UTC().Format(time.RFC3339Nano),
	})
	if err := p.Notif.Notify(ctx, db.NotifyQuotaWarning, string(payload)); err != nil {
		p.Log.Warn("meter: notify quota_warning", "err", err)
	}
	return true, p.WarningCommands.Complete(ctx, account.ID, result.IntentID, result.LeaseID)
}
