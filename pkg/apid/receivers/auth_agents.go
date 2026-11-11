package receivers

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// AgentRequests answers a gateway's agent approval and activity calls
// (ADR-20261111150000) with apid's own approval gate and store: apid is
// the only writer of both.
type AgentRequests interface {
	// DecideAgentRequest reports allow, with the approval the request went
	// through on, or the problem to answer with. state.ErrNotFound when the
	// key is not the account's.
	DecideAgentRequest(ctx context.Context, accountID, keyID, pattern string, binding state.AgentApprovalBinding, reason string) (allow bool, approvalID string, problem *api.Problem, err error)
	// RecordAgentActivity writes one activity row of the account's key.
	RecordAgentActivity(ctx context.Context, row state.AgentActivity) error
}

// WithAgentRequests lets the Auth service answer gateways' agent calls.
func WithAgentRequests(agents AgentRequests) AuthOption {
	return func(r *authReceiver) { r.agents = agents }
}

// AuthOption configures the Auth receiver.
type AuthOption func(*authReceiver)

// DecideAgentRequest runs the agent approval gate for a gateway.
func (r *authReceiver) DecideAgentRequest(ctx context.Context, req *apidpb.DecideAgentRequestRequest) (*apidpb.DecideAgentRequestResponse, error) {
	if r.agents == nil {
		return nil, status.Error(codes.Unavailable, "agent approvals are not configured")
	}
	if req.GetAccountId() == "" || req.GetKeyId() == "" || req.GetPattern() == "" || req.GetMethod() == "" || req.GetPath() == "" {
		return nil, status.Error(codes.InvalidArgument, "account_id, key_id, pattern, method and path are required")
	}
	binding := state.AgentApprovalBinding{KeyID: req.GetKeyId(), Method: req.GetMethod(), Path: req.GetPath(), BodySHA256: req.GetBodySha256()}
	allow, approvalID, problem, err := r.agents.DecideAgentRequest(ctx, req.GetAccountId(), req.GetKeyId(), req.GetPattern(), binding, api.SanitizeAgentReason(req.GetReason()))
	if errors.Is(err, state.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "no such key")
	}
	if err != nil {
		return nil, status.Error(codes.Unavailable, "could not check the request's approval")
	}
	resp := &apidpb.DecideAgentRequestResponse{Allow: allow, ApprovalId: approvalID}
	if !allow {
		if problem == nil {
			return nil, status.Error(codes.Internal, "refusal without a problem")
		}
		raw, err := json.Marshal(problem)
		if err != nil {
			return nil, status.Error(codes.Internal, "encode problem")
		}
		resp.Problem = raw
	}
	return resp, nil
}

// RecordAgentActivity writes one agent activity row for a gateway.
func (r *authReceiver) RecordAgentActivity(ctx context.Context, req *apidpb.RecordAgentActivityRequest) (*apidpb.RecordAgentActivityResponse, error) {
	if r.agents == nil {
		return nil, status.Error(codes.Unavailable, "agent activity is not configured")
	}
	if req.GetAccountId() == "" || req.GetKeyId() == "" || req.GetMethod() == "" || req.GetPath() == "" || req.GetStatus() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "account_id, key_id, method, path and status are required")
	}
	created := time.UnixMilli(req.GetCreatedAtUnixMs()).UTC()
	if req.GetCreatedAtUnixMs() <= 0 {
		created = time.Now().UTC()
	}
	err := r.agents.RecordAgentActivity(ctx, state.AgentActivity{
		KeyID: req.GetKeyId(), AccountID: req.GetAccountId(), Method: req.GetMethod(), Path: req.GetPath(),
		Status: int(req.GetStatus()), ApprovalID: req.GetApprovalId(), CreatedAt: created,
		Reason: api.SanitizeAgentReason(req.GetReason()),
	})
	if errors.Is(err, state.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "no such key")
	}
	if err != nil {
		return nil, status.Error(codes.Unavailable, "could not record agent activity")
	}
	return &apidpb.RecordAgentActivityResponse{}, nil
}
