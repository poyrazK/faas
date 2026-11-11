package apidgrpc

import (
	"context"
	"encoding/json"
	"fmt"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// AgentGate is how a gateway runs the agent approval gate and records agent
// activity: it asks apid, the only writer of both (ADR-20261111150000).
type AgentGate struct {
	Auth *AuthClientImpl
}

var (
	_ middleware.RemoteAgentGate       = AgentGate{}
	_ middleware.AgentActivityRecorder = AgentGate{}
)

// DecideAgentRequest asks apid to decide the key's request. An error
// (apid unreachable, or a release without the RPC) refuses the request.
func (g AgentGate) DecideAgentRequest(ctx context.Context, _ state.Account, key state.APIKey, pattern string, binding state.AgentApprovalBinding, reason string) (middleware.AgentGateDecision, error) {
	resp, err := g.Auth.cli.DecideAgentRequest(ctx, &apidpb.DecideAgentRequestRequest{
		AccountId: key.AccountID, KeyId: key.ID, Pattern: pattern,
		Method: binding.Method, Path: binding.Path, BodySha256: binding.BodySHA256, Reason: reason,
	})
	if err != nil {
		return middleware.AgentGateDecision{}, fmt.Errorf("apidgrpc: DecideAgentRequest: %w", err)
	}
	if resp.GetAllow() {
		return middleware.AgentGateDecision{Allow: true, ApprovalID: resp.GetApprovalId()}, nil
	}
	var problem api.Problem
	if err := json.Unmarshal(resp.GetProblem(), &problem); err != nil || problem.Status == 0 {
		return middleware.AgentGateDecision{}, fmt.Errorf("apidgrpc: decode approval problem: %w", err)
	}
	return middleware.AgentGateDecision{Problem: &problem}, nil
}

// RecordAgentActivity sends one activity row to apid.
func (g AgentGate) RecordAgentActivity(ctx context.Context, row state.AgentActivity) error {
	_, err := g.Auth.cli.RecordAgentActivity(ctx, &apidpb.RecordAgentActivityRequest{
		AccountId: row.AccountID, KeyId: row.KeyID, Method: row.Method, Path: row.Path,
		Status: int32(row.Status), ApprovalId: row.ApprovalID, Reason: row.Reason, CreatedAtUnixMs: row.CreatedAt.UnixMilli(),
	})
	if err != nil {
		return fmt.Errorf("apidgrpc: RecordAgentActivity: %w", err)
	}
	return nil
}
