package receivers

import (
	"context"
	"errors"
	"unicode/utf8"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
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
	case err != nil:
		return nil, status.Error(codes.Unavailable, "could not enroll the consumer")
	}
	return &apidpb.EnrollOAuthConsumerResponse{ConsumerId: consumer.ID, Active: consumer.Active(), Created: created}, nil
}
