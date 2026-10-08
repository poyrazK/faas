package canary

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
)

type ProfileGateRecoveryStore interface {
	ProfileCanaryGate(context.Context, CanaryRow) (api.ProfileCanaryGateDecision, error)
}

type profileGateRollbackClient interface {
	AbortProfileRegressedCanary(context.Context, string, int) (api.CanaryAdvanceResponse, error)
}

// A confirmed, explicitly enabled rollback does not wait for stage dwell.
// The internal request carries rollback intent so a policy race cannot turn
// this early recovery attempt into a traffic increase.
func (p *Progression) profileGateRecoveryReady(ctx context.Context, row CanaryRow, stats *Stats) bool {
	reader, ok := p.Store.(ProfileGateRecoveryStore)
	if !ok {
		return true
	}
	decision, err := reader.ProfileCanaryGate(ctx, row)
	if err != nil {
		p.Log.Warn("canary: profiling gate read failed", "deployment_id", row.ID, "err", err)
		stats.Errors++
		return false
	}
	if decision.Status != "regressed" || !decision.AutoRollback {
		return true
	}
	client, ok := p.APID.(profileGateRollbackClient)
	if !ok {
		stats.Errors++
		return false
	}
	response, err := client.AbortProfileRegressedCanary(ctx, row.ID, row.CanaryStep)
	if err != nil || response.ProfileGate == nil || response.ProfileGate.Status != "rolled_back" {
		p.Log.Warn("canary: profiling rollback held", "deployment_id", row.ID, "err", err)
		stats.Errors++
		return false
	}
	stats.Aborted++
	return false
}
