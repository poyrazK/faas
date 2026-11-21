package agentd

import (
	"context"
	"time"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/agentrunproto"
	"github.com/onebox-faas/faas/pkg/api"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

// creditStore is the store surface a run's approval-wait credit needs
// (ADR-20261027090016 amendment 3); *agentstore.Store implements it.
type creditStore interface {
	MarkRunningCredited(ctx context.Context, runID, token, nodeID, instanceID string, aiBudgetMillicents int64, creditMaxSec int) error
	RunApprovalCredit(ctx context.Context, runID, token string) (time.Duration, error)
}

var _ creditStore = (*agentstore.Store)(nil)

func (a *Agentd) creditStore() creditStore {
	cs, _ := a.store.(creditStore)
	return cs
}

// deadlineCredit is the credit a run may earn and the guest timeout that
// leaves room for it: the version must hold a tool in approve mode, and
// the guest's ceiling bounds the credit. A run with none keeps its timeout.
func deadlineCredit(claim agentstore.Claim) (creditMaxSec, guestTimeoutSec int) {
	if !holdsApproval(claim.CapabilityPolicy) {
		return 0, claim.TimeoutSec
	}
	creditMaxSec = max(min(api.AgentRunApprovalCreditMaxSec, agentrunproto.MaxTimeoutSec-claim.TimeoutSec), 0)
	return creditMaxSec, claim.TimeoutSec + creditMaxSec
}

func holdsApproval(policy *api.AgentCapabilityPolicy) bool {
	if policy == nil {
		return false
	}
	for _, t := range policy.Tools {
		if t.Mode == api.AgentCapabilityApprove {
			return true
		}
	}
	return false
}

// markRunning records the fence, with the run's credit cap when it has one.
func (r *run) markRunning(ctx context.Context, p *scheddpb.AgentRunPlaced) error {
	if cs := r.a.creditStore(); r.creditMaxSec > 0 && cs != nil {
		return cs.MarkRunningCredited(ctx, r.claim.RunID, r.claim.LeaseToken, p.GetNodeId(), p.GetInstance(), r.aiBudget, r.creditMaxSec)
	}
	return r.a.store.MarkRunning(ctx, r.claim.RunID, r.claim.LeaseToken, p.GetNodeId(), p.GetInstance(), r.aiBudget)
}

// grantCredit reads the run's approval waits until the run ends and sends
// vmmd, through schedd, each larger total. A failed read keeps the last
// total, which only leaves the deadline earlier.
func (r *run) grantCredit(ctx context.Context) {
	cs := r.a.creditStore()
	if cs == nil {
		return
	}
	poll := r.a.cfg.CreditPoll
	if poll <= 0 {
		poll = time.Duration(api.AgentRunApprovalCreditPollSec) * time.Second
	}
	limit := time.Duration(r.creditMaxSec) * time.Second
	tick := time.NewTicker(poll)
	defer tick.Stop()
	var sent time.Duration
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		credit, err := cs.RunApprovalCredit(ctx, r.claim.RunID, r.claim.LeaseToken)
		if err != nil {
			if ctx.Err() == nil {
				r.a.log.Warn("agentd: read approval credit", "run_id", r.claim.RunID, "err", err)
			}
			continue
		}
		credit = min(credit, limit).Truncate(time.Millisecond)
		if credit <= sent {
			continue
		}
		if err := r.send(&scheddpb.AgentRunRequest{Frame: &scheddpb.AgentRunRequest_Extend{Extend: &scheddpb.AgentRunExtend{
			CreditMs: credit.Milliseconds()}}}); err != nil {
			return
		}
		r.a.metrics.creditGranted(credit - sent)
		sent = credit
	}
}
