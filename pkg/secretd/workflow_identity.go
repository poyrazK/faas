package secretd

import (
	"context"

	"google.golang.org/grpc/codes"

	secretdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/secretd/v1"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/outbound"
)

// MintWorkflowIdentity implements secretdpb.SecretdServer (slice 3k-2): the
// workflow outbound identity token for one step attempt of an app the caller
// owns or holds a grant for, in that app's account. outboundd checks the run,
// attempt and request template against the database before it acts, so
// secretd binds only who may ask.
func (s *Server) MintWorkflowIdentity(ctx context.Context, req *secretdpb.MintWorkflowIdentityRequest) (*secretdpb.MintWorkflowIdentityResponse, error) {
	appID := req.GetAppId()
	if appID == "" || req.GetAccountId() == "" {
		return nil, rpcErr(codes.InvalidArgument, "secretd needs an app and an account")
	}
	if s.internalSvc == nil {
		return nil, rpcErr(codes.FailedPrecondition, "secretd holds no internal-service signing key")
	}
	_, _, via, err := s.authorizeByReference(ctx, rpcMintWorkflowIdentity, appID)
	if err != nil {
		return nil, err
	}
	target, err := s.store.AppTarget(ctx, appID)
	if err != nil || target.AccountID != req.GetAccountId() {
		s.metrics.refuse(rpcMintWorkflowIdentity, ReasonWrongAccount)
		return nil, rpcErr(codes.PermissionDenied, "secretd refused: the app is not in that account")
	}
	now := s.now()
	priv, kid, err := s.internalSvc.key(ctx, now)
	if err != nil {
		s.log.Error("secretd: internal-service key", "err", err)
		return nil, rpcErr(codes.Unavailable, "secretd could not load the internal-service signing key")
	}
	identity := outbound.WorkflowIdentity{
		AccountID: req.GetAccountId(), AppID: appID, PlatformTenantID: req.GetPlatformTenantId(),
		RunID: req.GetRunId(), StepName: req.GetStepName(), Attempt: int(req.GetAttempt()), AttemptToken: req.GetAttemptToken(),
	}
	request := outbound.WorkflowOutboundRequest{
		IntegrationID: req.GetIntegrationId(), Method: req.GetMethod(), Path: req.GetPath(), RawQuery: req.GetRawQuery(),
		PathTemplate: req.GetPathTemplate(), QueryTemplate: req.GetQueryTemplate(),
	}
	token, err := outbound.MintWorkflowIdentityForBodyHash(identity, request, req.GetBodySha256(), priv, kid, now)
	if err != nil {
		s.log.Warn("secretd: refused workflow identity", "app_id", logsanitize.Field(appID), "err", err)
		s.metrics.refuse(rpcMintWorkflowIdentity, ReasonEnvelope)
		return nil, rpcErr(codes.InvalidArgument, "secretd refused: malformed workflow identity")
	}
	s.metrics.open(rpcMintWorkflowIdentity, via)
	return &secretdpb.MintWorkflowIdentityResponse{Token: token}, nil
}
