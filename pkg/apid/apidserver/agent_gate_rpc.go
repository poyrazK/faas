package apidserver

import (
	"context"

	"github.com/onebox-faas/faas/pkg/agentapproval"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/receivers"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// agentRequestsForGateways answers a gateway's agent approval and activity
// calls with apid's own approval gate and store (ADR-20261111150000): apid
// is the only writer of approval requests and of the activity log.
type agentRequestsForGateways struct {
	mw    *middleware.Middleware
	store state.Store
}

var _ receivers.AgentRequests = agentRequestsForGateways{}

// agentRequestsForGateways returns nil before the auth middleware exists;
// the Auth service then answers gateways' agent calls Unavailable.
func (s *server) agentRequestsForGateways() receivers.AgentRequests {
	if s.authMw == nil || s.store == nil {
		return nil
	}
	return agentRequestsForGateways{mw: s.authMw, store: s.store}
}

// DecideAgentRequest classifies pattern itself, so a gateway cannot choose
// a laxer rule. A gateway asks only once it has found a production target
// for a rule that has one, so the target is not looked up again.
func (a agentRequestsForGateways) DecideAgentRequest(ctx context.Context, accountID, keyID, pattern string, binding state.AgentApprovalBinding, reason string) (bool, string, *api.Problem, error) {
	key, err := a.store.GetAPIKey(ctx, accountID, keyID)
	if err != nil {
		return false, "", nil, err
	}
	rule, gated := agentapproval.Classify(pattern)
	if !gated || !middleware.ProjectLimitable(key) {
		return true, "", nil, nil
	}
	acct, err := a.store.AccountByID(ctx, accountID)
	if err != nil {
		return false, "", nil, err
	}
	binding.KeyID = key.ID
	decision, err := a.mw.DecideAgentRequest(ctx, acct, key, rule, binding, reason)
	if err != nil {
		return false, "", nil, err
	}
	return decision.Allow, decision.ApprovalID, decision.Problem, nil
}

// RecordAgentActivity records a row of the account's own key.
func (a agentRequestsForGateways) RecordAgentActivity(ctx context.Context, row state.AgentActivity) error {
	key, err := a.store.GetAPIKey(ctx, row.AccountID, row.KeyID)
	if err != nil {
		return err
	}
	row.OrgID = key.OrgID
	return storeAuthAdapter{Store: a.store}.RecordAgentActivity(ctx, row)
}
