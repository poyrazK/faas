package receivers

import (
	"context"
	"errors"
	"time"
	"unicode/utf8"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/apid/consumersignup"
	"github.com/onebox-faas/faas/pkg/state"
)

// OAuthConsumers enrolls a gateway's verified OAuth callers as API
// consumers (ADR-20261124094745): apid is the only writer of consumers.
// apid's *consumersignup.Service.
type OAuthConsumers interface {
	// EnrollOAuthConsumer returns the consumer in's issuer and subject are
	// on in's app, creating it the first time, and reports created; the
	// callee sets in's times. state.ErrAPIConsumerOAuthOff,
	// state.ErrAPIConsumerSignupLimit and state.ErrNotFound.
	EnrollOAuthConsumer(ctx context.Context, in state.APIConsumerEnrollment) (state.APIConsumer, bool, error)
}

// UpgradeLinks mints a consumer's upgrade page link (ADR-20261124094745):
// apid's *consumersignup.Service.
type UpgradeLinks interface {
	// UpgradeLink returns "" when the app offers no upgrade, and
	// state.ErrNotFound when the app is not the account's.
	UpgradeLink(ctx context.Context, accountID, appID, consumerID string) (string, time.Time, error)
}

// WithUpgradeLinks lets the Auth service mint upgrade links.
func WithUpgradeLinks(links UpgradeLinks) AuthOption {
	return func(r *authReceiver) { r.upgrades = links }
}

// ConsumerUpgradeLink returns a consumer's upgrade page link for a gateway
// answering a plan-limit refusal.
func (r *authReceiver) ConsumerUpgradeLink(ctx context.Context, req *apidpb.ConsumerUpgradeLinkRequest) (*apidpb.ConsumerUpgradeLinkResponse, error) {
	if r.upgrades == nil {
		return nil, status.Error(codes.Unavailable, "upgrade links are not configured")
	}
	if req.GetAccountId() == "" || req.GetAppId() == "" || req.GetConsumerId() == "" {
		return nil, status.Error(codes.InvalidArgument, "account_id, app_id and consumer_id are required")
	}
	link, expires, err := r.upgrades.UpgradeLink(ctx, req.GetAccountId(), req.GetAppId(), req.GetConsumerId())
	switch {
	case errors.Is(err, state.ErrNotFound):
		return nil, status.Error(codes.NotFound, "no such app")
	case err != nil:
		return nil, status.Error(codes.Unavailable, "could not mint the upgrade link")
	}
	resp := &apidpb.ConsumerUpgradeLinkResponse{Url: link}
	if link != "" {
		resp.ExpiresAtUnixMs = expires.UnixMilli()
	}
	return resp, nil
}

// WithOAuthConsumers lets the Auth service enroll OAuth consumers.
func WithOAuthConsumers(consumers OAuthConsumers) AuthOption {
	return func(r *authReceiver) { r.consumers = consumers }
}

// EnrollOAuthConsumer returns the consumer a verified OAuth caller is.
func (r *authReceiver) EnrollOAuthConsumer(ctx context.Context, req *apidpb.EnrollOAuthConsumerRequest) (*apidpb.EnrollOAuthConsumerResponse, error) {
	if r.consumers == nil {
		return nil, status.Error(codes.Unavailable, "OAuth consumers are not configured")
	}
	if req.GetAccountId() == "" || req.GetAppId() == "" || req.GetIssuer() == "" || req.GetSubject() == "" {
		return nil, status.Error(codes.InvalidArgument, "account_id, app_id, issuer and subject are required")
	}
	if len(req.GetIssuer()) > state.MaxAPIConsumerIdentityIssuerLen || len(req.GetSubject()) > state.MaxAPIConsumerIdentitySubjectLen ||
		!utf8.ValidString(req.GetSubject()) {
		return nil, status.Error(codes.InvalidArgument, "issuer or subject is too long")
	}
	consumer, created, err := r.consumers.EnrollOAuthConsumer(ctx, state.APIConsumerEnrollment{
		AccountID: req.GetAccountId(), AppID: req.GetAppId(), Issuer: req.GetIssuer(), Subject: req.GetSubject(), Email: req.GetEmail()})
	switch {
	case errors.Is(err, state.ErrAPIConsumerOAuthOff):
		return nil, status.Error(codes.FailedPrecondition, "the app does not enroll OAuth callers")
	case errors.Is(err, state.ErrAPIConsumerSignupLimit):
		return nil, status.Error(codes.ResourceExhausted, "the app took its sign-ups for the day")
	case errors.Is(err, state.ErrNotFound):
		return nil, status.Error(codes.NotFound, "no such app or free plan")
	case errors.Is(err, state.ErrAPIConsumerIdentityInvalid):
		return nil, status.Error(codes.InvalidArgument, "invalid identity")
	case err != nil:
		return nil, status.Error(codes.Unavailable, "could not enroll the consumer")
	}
	resp := &apidpb.EnrollOAuthConsumerResponse{ConsumerId: consumer.ID, Active: consumer.Active(), Created: created}
	if r.accountTools != nil {
		resp.AccountTools = r.accountTools.AccountToolsOn(ctx, req.GetAccountId(), req.GetAppId())
	}
	return resp, nil
}

// AccountTools answers the account tools a gateway adds to an MCP server
// for its OAuth consumers (ADR-20261125194510): apid's
// *consumersignup.Service.
type AccountTools interface {
	// AccountToolsOn reports the app's opt-in.
	AccountToolsOn(ctx context.Context, accountID, appID string) bool
	// AccountTool returns tool's JSON answer for the consumer.
	// consumersignup.ErrAccountToolUnknown, consumersignup.ErrAccountToolsOff
	// and state.ErrNotFound.
	AccountTool(ctx context.Context, accountID, appID, consumerID, tool string) ([]byte, error)
}

// WithAccountTools lets the Auth service answer account tools.
func WithAccountTools(tools AccountTools) AuthOption {
	return func(r *authReceiver) { r.accountTools = tools }
}

// ConsumerAccountTool answers one account tool for a gateway.
func (r *authReceiver) ConsumerAccountTool(ctx context.Context, req *apidpb.ConsumerAccountToolRequest) (*apidpb.ConsumerAccountToolResponse, error) {
	if r.accountTools == nil {
		return nil, status.Error(codes.Unavailable, "account tools are not configured")
	}
	if req.GetAccountId() == "" || req.GetAppId() == "" || req.GetConsumerId() == "" {
		return nil, status.Error(codes.InvalidArgument, "account_id, app_id and consumer_id are required")
	}
	out, err := r.accountTools.AccountTool(ctx, req.GetAccountId(), req.GetAppId(), req.GetConsumerId(), req.GetTool())
	switch {
	case errors.Is(err, consumersignup.ErrAccountToolUnknown):
		return nil, status.Error(codes.InvalidArgument, "unknown account tool")
	case errors.Is(err, consumersignup.ErrAccountToolsOff):
		return nil, status.Error(codes.FailedPrecondition, "the app's account tools are off")
	case errors.Is(err, state.ErrNotFound):
		return nil, status.Error(codes.NotFound, "no such app or consumer")
	case err != nil:
		return nil, status.Error(codes.Unavailable, "could not answer the account tool")
	}
	return &apidpb.ConsumerAccountToolResponse{ResultJson: out}, nil
}

// OAuthLinks links a gateway's verified OAuth callers to the consumer whose
// key they proved (ADR-20261124094745): apid's *consumersignup.Service.
type OAuthLinks interface {
	// LinkOAuthConsumer returns the identity and the revoked enrolled
	// consumer's ID. state.ErrAPIConsumerKeyRejected,
	// state.ErrAPIConsumerOAuthOff, state.ErrConflict and state.ErrNotFound.
	LinkOAuthConsumer(ctx context.Context, in state.APIConsumerIdentityLink, key string) (state.APIConsumerIdentity, string, error)
}

// WithOAuthLinks lets the Auth service link OAuth callers by key.
func WithOAuthLinks(links OAuthLinks) AuthOption {
	return func(r *authReceiver) { r.links = links }
}

// LinkOAuthConsumer points a verified OAuth caller at a key's consumer.
func (r *authReceiver) LinkOAuthConsumer(ctx context.Context, req *apidpb.LinkOAuthConsumerRequest) (*apidpb.LinkOAuthConsumerResponse, error) {
	if r.links == nil {
		return nil, status.Error(codes.Unavailable, "OAuth consumer links are not configured")
	}
	if req.GetAccountId() == "" || req.GetAppId() == "" || req.GetIssuer() == "" || req.GetSubject() == "" || req.GetKey() == "" {
		return nil, status.Error(codes.InvalidArgument, "account_id, app_id, issuer, subject and key are required")
	}
	if len(req.GetIssuer()) > state.MaxAPIConsumerIdentityIssuerLen || len(req.GetSubject()) > state.MaxAPIConsumerIdentitySubjectLen ||
		!utf8.ValidString(req.GetSubject()) {
		return nil, status.Error(codes.InvalidArgument, "issuer or subject is too long")
	}
	identity, revoked, err := r.links.LinkOAuthConsumer(ctx, state.APIConsumerIdentityLink{AccountID: req.GetAccountId(),
		AppID: req.GetAppId(), Issuer: req.GetIssuer(), Subject: req.GetSubject(), Email: req.GetEmail()}, req.GetKey())
	switch {
	case errors.Is(err, state.ErrAPIConsumerKeyRejected):
		return nil, status.Error(codes.PermissionDenied, "the key is not a live key of this app's consumers")
	case errors.Is(err, state.ErrAPIConsumerIdentityInvalid):
		return nil, status.Error(codes.InvalidArgument, "invalid identity")
	case errors.Is(err, state.ErrAPIConsumerOAuthOff):
		return nil, status.Error(codes.FailedPrecondition, "the app does not enroll OAuth callers")
	case errors.Is(err, state.ErrConflict):
		return nil, status.Error(codes.AlreadyExists, "the identity is linked to another consumer; the app owner unlinks it first")
	case errors.Is(err, state.ErrNotFound):
		return nil, status.Error(codes.NotFound, "no such app")
	case err != nil:
		return nil, status.Error(codes.Unavailable, "could not link the consumer")
	}
	return &apidpb.LinkOAuthConsumerResponse{ConsumerId: identity.ConsumerID, RevokedConsumerId: revoked}, nil
}
